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
	want := []string{"001_core.sql", "002_media_job_leases.sql", "003_media_sources.sql", "004_media_perceptual_hash_lookup.sql"}
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
}
