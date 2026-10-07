package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunRejectsInvalidBootstrapArgumentsBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--user-id", "0"},
		{"--user-id", "-1"},
		{"--user-id", "12", "extra"},
	} {
		if err := run(context.Background(), args, func(string) string { return "" }, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "--user-id") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	err := run(context.Background(), []string{"--user-id", "12"}, func(string) string { return "" }, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err=%v", err)
	}
}
