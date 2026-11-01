package services

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// replayHunks reconstructs the new file from hunks + old file using real
// unified-diff semantics: rows between hunks are unchanged old rows, inside a
// hunk context/del consume old rows contiguously, add/context emit.
func replayHunks(t *testing.T, hunks []FileDiffHunk, a []string) []string {
	t.Helper()
	rebuilt := []string{}
	oi := 0 // 下一行待消费的旧行（0 基）
	for hi, h := range hunks {
		// hunk 之前的未变行直接照抄。
		var firstOld int
		for _, l := range h.Lines {
			if l.Type != DiffLineAdd {
				firstOld = l.OldNo
				break
			}
		}
		for oi < firstOld-1 {
			rebuilt = append(rebuilt, a[oi])
			oi++
		}
		for _, l := range h.Lines {
			switch l.Type {
			case DiffLineContext, DiffLineDel:
				if l.OldNo != oi+1 {
					t.Fatalf("hunk %d: oldNo %d not contiguous (next %d)", hi, l.OldNo, oi+1)
				}
				if a[l.OldNo-1] != l.Text {
					t.Fatalf("hunk %d: old row %d text mismatch", hi, l.OldNo)
				}
				oi++
				if l.Type == DiffLineContext {
					rebuilt = append(rebuilt, l.Text)
				}
			case DiffLineAdd:
				if l.OldNo != 0 {
					t.Fatalf("hunk %d: add row carries oldNo %d", hi, l.OldNo)
				}
				rebuilt = append(rebuilt, l.Text)
			}
		}
	}
	for oi < len(a) {
		rebuilt = append(rebuilt, a[oi])
		oi++
	}
	return rebuilt
}

func assertDiffRoundTrip(t *testing.T, a, b []string) {
	t.Helper()
	ops := lineDiff(a, b)
	hunks, added, removed, truncated := groupHunks(ops, a, b)
	rebuilt := replayHunks(t, hunks, a)
	if strings.Join(rebuilt, "\n") != strings.Join(b, "\n") {
		t.Fatalf("reconstruction mismatch (added %d removed %d truncated %v)", added, removed, truncated)
	}
	// added/removed 计数与行类型一致。
	var countAdd, countDel int
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Type {
			case DiffLineAdd:
				countAdd++
			case DiffLineDel:
				countDel++
			}
		}
	}
	if !truncated && (countAdd != added || countDel != removed) {
		t.Fatalf("counts mismatch: rendered %d/%d vs totals %d/%d", countAdd, countDel, added, removed)
	}
}

// 随机小文件覆盖 DP 路径的全形态（插入/删除/替换/重复行）。
func TestLineDiffPropertySmall(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		n := rng.Intn(250)
		a := make([]string, n)
		for i := range a {
			a[i] = fmt.Sprintf("l%d", rng.Intn(40))
		}
		b := append([]string(nil), a...)
		for k := rng.Intn(30); k > 0; k-- {
			switch rng.Intn(3) {
			case 0:
				pos := rng.Intn(len(b) + 1)
				b = append(b[:pos], append([]string{fmt.Sprintf("n%d", rng.Intn(40))}, b[pos:]...)...)
			case 1:
				if len(b) > 0 {
					pos := rng.Intn(len(b))
					b = append(b[:pos], b[pos+1:]...)
				}
			case 2:
				if len(b) > 0 {
					b[rng.Intn(len(b))] = fmt.Sprintf("r%d", rng.Intn(40))
				}
			}
		}
		assertDiffRoundTrip(t, a, b)
	}
}

// 大文件 + 局部修改覆盖分块路径：远超 DP 上限，变化集中在随机窗口。
func TestLineDiffPropertyChunked(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 15; iter++ {
		n := 4000 + rng.Intn(4000)
		a := make([]string, n)
		for i := range a {
			a[i] = fmt.Sprintf("line-%d", i)
		}
		b := append([]string(nil), a...)
		for w := rng.Intn(5); w >= 0; w-- {
			center := rng.Intn(len(b))
			span := 1 + rng.Intn(800)
			lo := center - span/2
			if lo < 0 {
				lo = 0
			}
			hi := lo + span
			if hi > len(b) {
				hi = len(b)
			}
			var repl []string
			for i := lo; i < hi; i++ {
				switch rng.Intn(4) {
				case 0:
					// 删除该行
				default:
					repl = append(repl, fmt.Sprintf("v%d-%d", iter, i))
				}
			}
			b = append(b[:lo], append(repl, b[hi:]...)...)
		}
		assertDiffRoundTrip(t, a, b)
	}
}
