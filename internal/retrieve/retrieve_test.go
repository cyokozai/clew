package retrieve

import (
	"path/filepath"
	"testing"

	"github.com/cyokozai/clew/internal/index"
)

func buildIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	files := map[string]string{
		"astropy/modeling/separable.py": "def separability_matrix(transform):\n    return _separable(transform)\n\ndef _cstack(left, right):\n    pass\n",
		"astropy/modeling/core.py":      "class Model:\n    def __call__(self):\n        pass\n",
		"docs/install.rst.txt":          "Install astropy with pip. Nothing about matrices here.\n",
		"astropy/io/fits/header.py":     "class Header:\n    def tostring(self):\n        return ''\n",
	}
	for p, b := range files {
		if err := idx.AddFile(p, b); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func TestSearchRanksIdentifierOwnerFirst(t *testing.T) {
	idx := buildIndex(t)
	body := "Modeling's `separability_matrix` does not compute separability correctly " +
		"for nested CompoundModels. See astropy/modeling/separable.py."

	hits, err := Search(idx, body, 10)
	if err != nil {
		t.Fatalf("Search が失敗: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("候補が空")
	}
	if hits[0].Path != "astropy/modeling/separable.py" {
		t.Errorf("1 位 = %q, want astropy/modeling/separable.py (全体: %v)", hits[0].Path, hits)
	}
	if hits[0].Score <= 0 {
		t.Errorf("スコアは正のはず: %v", hits[0].Score)
	}
}

func TestSearchSurvivesSymbolOnlyBody(t *testing.T) {
	idx := buildIndex(t)
	for _, body := range []string{"", "   ", `*** "" ^^ (:) --`, "^*(:)"} {
		hits, err := Search(idx, body, 5)
		if err != nil {
			t.Fatalf("記号だけの本文で失敗した (%q): %v", body, err)
		}
		if len(hits) != 0 {
			t.Errorf("記号だけの本文で候補が出た (%q): %v", body, hits)
		}
	}
}

func TestSearchRespectsK(t *testing.T) {
	idx := buildIndex(t)
	hits, err := Search(idx, "astropy modeling matrix header install", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 2 {
		t.Errorf("件数 = %d, want <= 2", len(hits))
	}
}

func TestBuildQueryDropsSpecialCharacters(t *testing.T) {
	got := buildQuery([]string{`"foo*"`, "bar(baz)", "^-:", "ab"})
	want := "foo OR bar OR baz"
	if got != want {
		t.Errorf("buildQuery = %q, want %q", got, want)
	}
	if buildQuery([]string{`***`, "^"}) != "" {
		t.Error("空になった問合せは空文字を返すはず")
	}
}

func TestCodeTermsPicksIdentifiers(t *testing.T) {
	body := "Calling `separability_matrix` raises ValueError in astropy/modeling/separable.py " +
		"when CompoundModel is nested."
	got := codeTerms(body)
	want := map[string]bool{
		"separability_matrix":           false,
		"astropy/modeling/separable.py": false,
		"ValueError":                    false,
		"CompoundModel":                 false,
	}
	for _, g := range got {
		if _, ok := want[g]; ok {
			want[g] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("コード由来の語 %q を拾えていない: %v", k, got)
		}
	}
}

func TestProseTermsDropsStopwords(t *testing.T) {
	for _, term := range proseTerms("the and for that this with issue bug") {
		t.Errorf("停止語が残っている: %q", term)
	}
}
