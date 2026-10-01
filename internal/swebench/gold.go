package swebench

import "strings"

// Gold は patch から取り出した正解ファイルの集合。
// A は変更前（a 側）、B は変更後（b 側）のパス。
type Gold struct {
	A []string
	B []string
}

// Side は goldSide の指定に応じて片側を返す。既定は a 側。
func (g Gold) Side(side string) []string {
	if strings.EqualFold(side, "b") {
		return g.B
	}
	return g.A
}

type fileBlock struct {
	gitA, gitB string
	a, b       string
	aSeen      bool
	bSeen      bool
}

// GoldFiles は unified diff を走査して両側のパスを返す。/dev/null は除く。
func GoldFiles(patch string) Gold {
	var blocks []*fileBlock
	var cur *fileBlock

	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			cur = &fileBlock{}
			blocks = append(blocks, cur)
			rest := strings.TrimPrefix(line, "diff --git ")
			if a, b, ok := splitGitPair(rest); ok {
				cur.gitA, cur.gitB = a, b
			}
		case cur == nil:
			// diff --git の前に現れる行は読み飛ばす。
		case strings.HasPrefix(line, "rename from "):
			cur.gitA = trimPrefixPath(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			cur.gitB = trimPrefixPath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "--- "):
			cur.aSeen = true
			cur.a = trimPrefixPath(cleanDiffPath(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			cur.bSeen = true
			cur.b = trimPrefixPath(cleanDiffPath(strings.TrimPrefix(line, "+++ ")))
		}
	}

	var g Gold
	seenA := map[string]bool{}
	seenB := map[string]bool{}
	for _, b := range blocks {
		a := b.a
		if !b.aSeen {
			a = b.gitA
		}
		bb := b.b
		if !b.bSeen {
			bb = b.gitB
		}
		if a != "" && !seenA[a] {
			seenA[a] = true
			g.A = append(g.A, a)
		}
		if bb != "" && !seenB[bb] {
			seenB[bb] = true
			g.B = append(g.B, bb)
		}
	}
	return g
}

// splitGitPair は "a/foo b/foo" を分ける。空白を含むパスは扱わない。
func splitGitPair(rest string) (string, string, bool) {
	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return "", "", false
	}
	return trimPrefixPath(fields[0]), trimPrefixPath(fields[1]), true
}

// cleanDiffPath は ---/+++ 行のパスから、後ろのタイムスタンプを落とす。
func cleanDiffPath(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// trimPrefixPath は a/ b/ の接頭辞を剥がし、/dev/null を空に潰す。
func trimPrefixPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "/dev/null" || s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, `"`)
	s = strings.TrimSuffix(s, `"`)
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}
