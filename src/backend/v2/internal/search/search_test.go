package search

import (
	"reflect"
	"testing"
)

func TestParseTagsAndScore(t *testing.T) {
	got, err := Parse(`cat landscape -anime score:>=100`)
	if err != nil {
		t.Fatal(err)
	}
	want := Query{
		IncludeTags: []string{"cat", "landscape"},
		ExcludeTags: []string{"anime"},
		Score:       &ScorePredicate{Op: ScoreGTE, Value: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestParseQuotedTagAndDeduplicates(t *testing.T) {
	got, err := Parse(`"Long Tag" LONG\ TAG`)
	if err == nil {
		t.Fatalf("expected invalid unquoted backslash, got %#v", got)
	}

	got, err = Parse(`"Long Tag" "long tag" -ANIME -anime`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.IncludeTags, []string{"long tag"}) || !reflect.DeepEqual(got.ExcludeTags, []string{"anime"}) {
		t.Fatalf("unexpected normalized tags: %#v", got)
	}
}

func TestParseRejectsMalformedPredicates(t *testing.T) {
	for _, input := range []string{
		`score:100`,
		`score:!=100`,
		`score:>=`,
		`score:>=100 score:<200`,
		`-score:>=100`,
		`-`,
		`"unterminated`,
	} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", input)
		}
	}
}

func TestParseNegativeScore(t *testing.T) {
	got, err := Parse(`score:<-10`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Score == nil || got.Score.Op != ScoreLT || got.Score.Value != -10 {
		t.Fatalf("unexpected score: %#v", got.Score)
	}
}
