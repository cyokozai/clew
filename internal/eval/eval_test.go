package eval

import (
	"math"
	"testing"
	"time"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScoreCountsRecallAndAcc(t *testing.T) {
	gold := []string{"a.py", "b.py"}
	ranked := []string{"x.py", "a.py", "y.py", "z.py", "b.py", "w.py"}

	recall, acc := Score(gold, ranked, []int{1, 5, 10})

	if !almost(recall[1], 0) {
		t.Errorf("recall@1 = %v, want 0", recall[1])
	}
	if !almost(acc[1], 0) {
		t.Errorf("acc@1 = %v, want 0", acc[1])
	}
	if !almost(recall[5], 1.0) {
		t.Errorf("recall@5 = %v, want 1.0 (a.py と b.py の 2 件とも上位 5 に入る)", recall[5])
	}
	if !almost(acc[5], 1) {
		t.Errorf("acc@5 = %v, want 1", acc[5])
	}
	if !almost(recall[10], 1.0) {
		t.Errorf("recall@10 = %v, want 1.0", recall[10])
	}
}

func TestScorePartialRecall(t *testing.T) {
	gold := []string{"a.py", "b.py", "c.py"}
	ranked := []string{"a.py", "x.py"}
	recall, acc := Score(gold, ranked, []int{1, 5})
	if !almost(recall[1], 1.0/3.0) {
		t.Errorf("recall@1 = %v, want 1/3", recall[1])
	}
	if !almost(acc[1], 1) {
		t.Errorf("acc@1 = %v, want 1", acc[1])
	}
	if !almost(recall[5], 1.0/3.0) {
		t.Errorf("recall@5 = %v, want 1/3", recall[5])
	}
}

func TestScoreNoHit(t *testing.T) {
	recall, acc := Score([]string{"a.py"}, []string{"x.py", "y.py"}, []int{1, 5})
	for _, k := range []int{1, 5} {
		if !almost(recall[k], 0) || !almost(acc[k], 0) {
			t.Errorf("k=%d: recall=%v acc=%v, want 0", k, recall[k], acc[k])
		}
	}
}

func TestScoreIgnoresDuplicateHits(t *testing.T) {
	recall, _ := Score([]string{"a.py", "b.py"}, []string{"a.py", "a.py", "a.py"}, []int{5})
	if !almost(recall[5], 0.5) {
		t.Errorf("recall@5 = %v, want 0.5（同じファイルを二重に数えない）", recall[5])
	}
}

func TestMedian(t *testing.T) {
	if got := Median(nil); got != 0 {
		t.Errorf("空なら 0 のはず: %v", got)
	}
	odd := []time.Duration{30 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	if got := Median(odd); got != 20*time.Millisecond {
		t.Errorf("Median(odd) = %v, want 20ms", got)
	}
	even := []time.Duration{40 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	if got := Median(even); got != 25*time.Millisecond {
		t.Errorf("Median(even) = %v, want 25ms", got)
	}
}

func TestRunOnEmptyInstances(t *testing.T) {
	old := Logf
	Logf = func(string, ...any) {}
	t.Cleanup(func() { Logf = old })

	res, err := Run(t.Context(), nil, []int{1, 5, 10}, "a")
	if err != nil {
		t.Fatalf("Run が失敗: %v", err)
	}
	if res.N != 0 || res.MedianLatency != 0 {
		t.Errorf("空の入力で %+v", res)
	}
}
