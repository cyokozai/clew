package index

import (
	"path/filepath"
	"testing"
)

func TestOpenAddAndQuery(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a-b-sha.db")
	idx, err := Open(p)
	if err != nil {
		t.Fatalf("Open が失敗: %v", err)
	}
	if !idx.Fresh {
		t.Error("新規作成なので Fresh は true のはず")
	}
	if err := idx.AddFile("pkg/mod.py", "def separability_matrix():\n    return 1\n"); err != nil {
		t.Fatalf("AddFile が失敗: %v", err)
	}
	if err := idx.AddFile("pkg/other.py", "def unrelated():\n    pass\n"); err != nil {
		t.Fatalf("AddFile が失敗: %v", err)
	}

	n, err := idx.Count()
	if err != nil {
		t.Fatalf("Count が失敗: %v", err)
	}
	if n != 2 {
		t.Fatalf("行数 = %d, want 2", n)
	}

	var path string
	row := idx.DB().QueryRow(`SELECT path FROM files WHERE files MATCH ? ORDER BY rank LIMIT 1`, "separability_matrix")
	if err := row.Scan(&path); err != nil {
		t.Fatalf("入れたものを引けない: %v", err)
	}
	if path != "pkg/mod.py" {
		t.Errorf("path = %q, want pkg/mod.py", path)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close が失敗: %v", err)
	}
}

func TestOpenDoesNotRebuildExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a-b-sha.db")
	idx, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.AddFile("x.py", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(p)
	if err != nil {
		t.Fatalf("再オープンが失敗: %v", err)
	}
	defer again.Close()
	if again.Fresh {
		t.Error("既存の索引なので Fresh は false のはず")
	}
	n, err := again.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("行数 = %d, want 1（作り直されている）", n)
	}
}

func TestPathFor(t *testing.T) {
	got, err := PathFor("/root", "astropy", "astropy", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/root", "astropy-astropy-abc123.db")
	if got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
}
