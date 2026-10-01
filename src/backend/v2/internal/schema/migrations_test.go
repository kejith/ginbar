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
	want := []string{"001_core.sql"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %#v want %#v", names, want)
	}
	body, err := Migration(names[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("CREATE TABLE users"),
		[]byte("CREATE TABLE posts"),
		[]byte("CREATE TABLE media_jobs"),
		[]byte("posts_feed_released_idx"),
	} {
		if !bytes.Contains(body, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
}
