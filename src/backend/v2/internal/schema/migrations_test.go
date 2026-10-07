package schema

import (
	"bytes"
	"reflect"
	"testing"
)

func TestMigrationSet(t *testing.T) {
	names, err := MigrationNames()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"001_core.sql",
		"002_media_job_leases.sql",
		"003_media_sources.sql",
		"004_media_perceptual_hash_lookup.sql",
		"005_media_job_regeneration_lookup.sql",
		"006_auth_sessions.sql",
		"007_moderation_audit.sql",
		"008_private_messages.sql",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %#v want %#v", names, want)
	}

	core, err := Migration(names[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("CREATE TABLE users"),
		[]byte("CREATE TABLE posts"),
		[]byte("CREATE TABLE media_jobs"),
		[]byte("posts_feed_released_idx"),
	} {
		if !bytes.Contains(core, required) {
			t.Fatalf("core migration missing %q", required)
		}
	}

	leases, err := Migration(names[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("lease_generation"),
		[]byte("media_jobs_running_lease_check"),
		[]byte("media_jobs_runnable_idx"),
	} {
		if !bytes.Contains(leases, required) {
			t.Fatalf("lease migration missing %q", required)
		}
	}

	sources, err := Migration(names[2])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("CREATE TABLE media_sources"),
		[]byte("media_sources_origin_check"),
		[]byte("media_sources_sha256_idx"),
	} {
		if !bytes.Contains(sources, required) {
			t.Fatalf("source migration missing %q", required)
		}
	}

	perceptualLookup, err := Migration(names[3])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("media_phash_post_idx"),
		[]byte("(perceptual_hash, post_id DESC)"),
		[]byte("DROP INDEX media_phash_idx"),
	} {
		if !bytes.Contains(perceptualLookup, required) {
			t.Fatalf("perceptual lookup migration missing %q", required)
		}
	}

	regenerationLookup, err := Migration(names[4])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("media_jobs_post_kind_id_idx"),
		[]byte("(post_id, kind, id DESC)"),
	} {
		if !bytes.Contains(regenerationLookup, required) {
			t.Fatalf("regeneration lookup migration missing %q", required)
		}
	}

	authSessions, err := Migration(names[5])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("CREATE TABLE user_sessions"),
		[]byte("token_hash bytea NOT NULL UNIQUE"),
		[]byte("user_id bigint NOT NULL REFERENCES users(id)"),
		[]byte("expires_at timestamptz NOT NULL"),
		[]byte("revoked_at timestamptz"),
	} {
		if !bytes.Contains(authSessions, required) {
			t.Fatalf("auth sessions migration missing %q", required)
		}
	}

	moderationAudit, err := Migration(names[6])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("posts_moderation_consistent"),
		[]byte("comments_moderation_consistent"),
		[]byte("moderated_by_user_id bigint REFERENCES users(id)"),
	} {
		if !bytes.Contains(moderationAudit, required) {
			t.Fatalf("moderation audit migration missing %q", required)
		}
	}

	privateMessages, err := Migration(names[7])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("CREATE TABLE private_messages"),
		[]byte("sender_user_id bigint NOT NULL REFERENCES users(id)"),
		[]byte("recipient_user_id bigint NOT NULL REFERENCES users(id)"),
		[]byte("private_messages_distinct_users"),
		[]byte("private_messages_body_length"),
		[]byte("private_messages_thread_idx"),
		[]byte("(sender_user_id, recipient_user_id, id DESC)"),
	} {
		if !bytes.Contains(privateMessages, required) {
			t.Fatalf("private messages migration missing %q", required)
		}
	}
}
