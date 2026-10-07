package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/httpapi"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

type regenerationHTTPResponse struct {
	PostID  int64              `json:"postId"`
	JobID   int64              `json:"jobId"`
	Outcome regenerate.Outcome `json:"outcome"`
}

func TestHTTPRegenerationUsesAuthoritativeAdminMutationPath(t *testing.T) {
	for _, tc := range []struct {
		name        string
		initial     int16
		wantOutcome regenerate.Outcome
	}{
		{name: "succeeded", initial: mediaJobStateSucceeded, wantOutcome: regenerate.OutcomeQueued},
		{name: "failed", initial: mediaJobStateFailed, wantOutcome: regenerate.OutcomeQueued},
		{name: "pending", initial: mediaJobStatePending, wantOutcome: regenerate.OutcomeCoalesced},
		{name: "running", initial: mediaJobStateRunning, wantOutcome: regenerate.OutcomeSuperseded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := testIngestionStore(t)
			defer cleanup()
			ctx := context.Background()
			fixtureState := tc.initial
			if fixtureState == mediaJobStateRunning {
				fixtureState = mediaJobStateSucceeded
			}
			fixture := readyRegenerationFixture(
				t,
				store,
				"http-regen-target-"+tc.name,
				fixtureState,
			)
			adminID := regenerationActor(t, store, "http-regen-admin-"+tc.name, role.Admin)

			var pendingAvailableAt time.Time
			switch tc.initial {
			case mediaJobStatePending:
				if _, err := store.pool.Exec(ctx, `
					UPDATE media_jobs
					SET attempts = 2,
					    available_at = clock_timestamp() + interval '1 hour',
					    last_error = 'retry later',
					    lease_generation = 4
					WHERE id = $1
				`, fixture.jobID); err != nil {
					t.Fatal(err)
				}
				if err := store.pool.QueryRow(
					ctx,
					"SELECT available_at FROM media_jobs WHERE id=$1",
					fixture.jobID,
				).Scan(&pendingAvailableAt); err != nil {
					t.Fatal(err)
				}
			case mediaJobStateRunning:
				if _, err := store.pool.Exec(ctx, `
					UPDATE media_jobs
					SET state = 1,
					    attempts = 3,
					    claimed_at = clock_timestamp(),
					    claimed_by = 'http-old-worker',
					    lease_expires_at = clock_timestamp() + interval '1 minute',
					    lease_generation = 7,
					    last_error = 'old attempt'
					WHERE id = $1
				`, fixture.jobID); err != nil {
					t.Fatal(err)
				}
			}

			server, cookie := regenerationAuthenticatedServer(t, store, adminID, byte(0x30+tc.initial))
			req := httptest.NewRequest(
				http.MethodPost,
				"http://ginbar.test/api/v2/admin/posts/"+strconv.FormatInt(fixture.postID, 10)+"/regeneration",
				nil,
			)
			req.Header.Set("Origin", "http://ginbar.test")
			req.AddCookie(&http.Cookie{Name: "ginbar_session", Value: cookie})
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusAccepted {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}

			var response regenerationHTTPResponse
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.PostID != fixture.postID ||
				response.JobID != fixture.jobID ||
				response.Outcome != tc.wantOutcome {
				t.Fatalf("response=%#v", response)
			}

			var (
				state           int16
				attempts        int32
				availableAt     time.Time
				claimedBy       *string
				leaseExpiresAt  *time.Time
				generation      int64
				lastError       *string
				releaseState    int16
				processingState int16
				storageKey      string
			)
			if err := store.pool.QueryRow(ctx, `
				SELECT
					job.state,
					job.attempts,
					job.available_at,
					job.claimed_by,
					job.lease_expires_at,
					job.lease_generation,
					job.last_error,
					post.release_state,
					media.processing_state,
					media.storage_key
				FROM media_jobs AS job
				JOIN posts AS post ON post.id = job.post_id
				JOIN media ON media.post_id = job.post_id
				WHERE job.id = $1
			`, fixture.jobID).Scan(
				&state,
				&attempts,
				&availableAt,
				&claimedBy,
				&leaseExpiresAt,
				&generation,
				&lastError,
				&releaseState,
				&processingState,
				&storageKey,
			); err != nil {
				t.Fatal(err)
			}
			if state != mediaJobStatePending ||
				releaseState != 1 ||
				processingState != 1 ||
				storageKey != fixture.key {
				t.Fatalf(
					"durable state=%d release=%d processing=%d key=%q",
					state,
					releaseState,
					processingState,
					storageKey,
				)
			}

			switch tc.initial {
			case mediaJobStatePending:
				if attempts != 2 ||
					!availableAt.Equal(pendingAvailableAt) ||
					generation != 4 ||
					lastError == nil ||
					*lastError != "retry later" {
					t.Fatalf(
						"pending state reset: attempts=%d available=%v/%v generation=%d error=%v",
						attempts,
						pendingAvailableAt,
						availableAt,
						generation,
						lastError,
					)
				}
			case mediaJobStateRunning:
				if attempts != 0 ||
					generation != 8 ||
					claimedBy != nil ||
					leaseExpiresAt != nil ||
					lastError != nil {
					t.Fatalf(
						"running state not superseded: attempts=%d generation=%d claimed=%v lease=%v error=%v",
						attempts,
						generation,
						claimedBy,
						leaseExpiresAt,
						lastError,
					)
				}
			default:
				if attempts != 0 || claimedBy != nil || leaseExpiresAt != nil || lastError != nil {
					t.Fatalf(
						"terminal state not reset: attempts=%d claimed=%v lease=%v error=%v",
						attempts,
						claimedBy,
						leaseExpiresAt,
						lastError,
					)
				}
			}
		})
	}
}

func TestHTTPRegenerationRejectsNonAdminAndCrossOriginWithoutDurableChange(t *testing.T) {
	for _, tc := range []struct {
		name        string
		actorRole   int16
		crossOrigin bool
		wantCode    int
	}{
		{name: "member", actorRole: role.Member, wantCode: http.StatusForbidden},
		{name: "moderator", actorRole: role.Moderator, wantCode: http.StatusForbidden},
		{name: "cross-origin-admin", actorRole: role.Admin, crossOrigin: true, wantCode: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := testIngestionStore(t)
			defer cleanup()
			ctx := context.Background()
			fixture := readyRegenerationFixture(
				t,
				store,
				"hr-target-"+tc.name,
				mediaJobStateSucceeded,
			)
			actorID := regenerationActor(t, store, "hr-actor-"+tc.name, tc.actorRole)
			server, cookie := regenerationAuthenticatedServer(t, store, actorID, byte(0x50+tc.actorRole))

			req := httptest.NewRequest(
				http.MethodPost,
				"http://ginbar.test/api/v2/admin/posts/"+strconv.FormatInt(fixture.postID, 10)+"/regeneration",
				nil,
			)
			if tc.crossOrigin {
				req.Header.Set("Origin", "https://evil.test")
			} else {
				req.Header.Set("Origin", "http://ginbar.test")
			}
			req.AddCookie(&http.Cookie{Name: "ginbar_session", Value: cookie})
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != tc.wantCode {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}

			var state int16
			var attempts int32
			var generation int64
			if err := store.pool.QueryRow(
				ctx,
				"SELECT state, attempts, lease_generation FROM media_jobs WHERE id=$1",
				fixture.jobID,
			).Scan(&state, &attempts, &generation); err != nil {
				t.Fatal(err)
			}
			if state != mediaJobStateSucceeded || attempts != 1 || generation != 0 {
				t.Fatalf(
					"rejected HTTP mutation changed job: state=%d attempts=%d generation=%d",
					state,
					attempts,
					generation,
				)
			}
		})
	}
}

func regenerationAuthenticatedServer(
	t *testing.T,
	store *Store,
	userID int64,
	fill byte,
) (*httpapi.Server, string) {
	t.Helper()
	rawToken := bytes.Repeat([]byte{fill}, 32)
	tokenHash := sha256.Sum256(rawToken)
	now := time.Now().UTC()
	if err := store.CreateSession(context.Background(), userID, tokenHash, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cfg := httpapi.DefaultConfig()
	cfg.CookieSecure = false
	return httpapi.NewWithConfig(store, cfg), base64.RawURLEncoding.EncodeToString(rawToken)
}
