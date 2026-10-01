package swebench

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("fixture を読めない: %v", err)
	}
	return string(b)
}

func TestGoldFiles(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		wantA   []string
		wantB   []string
	}{
		{
			name:    "通常の変更",
			fixture: "patch_modify.diff",
			wantA:   []string{"astropy/modeling/separable.py"},
			wantB:   []string{"astropy/modeling/separable.py"},
		},
		{
			name:    "新規ファイル",
			fixture: "patch_newfile.diff",
			wantA:   nil,
			wantB:   []string{"sympy/core/_print_helpers.py"},
		},
		{
			name:    "リネーム",
			fixture: "patch_rename.diff",
			wantA:   []string{"src/old_name.py"},
			wantB:   []string{"src/new_name.py"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GoldFiles(readFixture(t, tc.fixture))
			if !reflect.DeepEqual(got.A, tc.wantA) {
				t.Errorf("A = %v, want %v", got.A, tc.wantA)
			}
			if !reflect.DeepEqual(got.B, tc.wantB) {
				t.Errorf("B = %v, want %v", got.B, tc.wantB)
			}
		})
	}
}

func TestGoldFilesMultipleFiles(t *testing.T) {
	patch := readFixture(t, "patch_modify.diff") + readFixture(t, "patch_rename.diff")
	got := GoldFiles(patch)
	wantA := []string{"astropy/modeling/separable.py", "src/old_name.py"}
	if !reflect.DeepEqual(got.A, wantA) {
		t.Errorf("A = %v, want %v", got.A, wantA)
	}
}

func TestGoldSide(t *testing.T) {
	g := Gold{A: []string{"a.py"}, B: []string{"b.py"}}
	if got := g.Side("a"); got[0] != "a.py" {
		t.Errorf("Side(a) = %v", got)
	}
	if got := g.Side("b"); got[0] != "b.py" {
		t.Errorf("Side(b) = %v", got)
	}
	if got := g.Side(""); got[0] != "a.py" {
		t.Errorf("既定は a 側のはず: %v", got)
	}
}

func TestGoldFilesIgnoresDevNull(t *testing.T) {
	g := GoldFiles(readFixture(t, "patch_newfile.diff"))
	for _, p := range append(g.A, g.B...) {
		if p == "/dev/null" || p == "" {
			t.Errorf("/dev/null が混ざっている: %v", g)
		}
	}
}
