package gamedig

import (
	"math"
	"testing"

	"github.com/hostsrc/go-gamedig/internal/ase"
)

func TestJSInt(t *testing.T) {
	for in, want := range map[float64]int{
		math.NaN(): 0, 12: 12, -3: -3, math.Copysign(0, -1): 0,
		math.Inf(1): math.MaxInt, math.Inf(-1): math.MinInt,
	} {
		if got := jsInt(in); got != want {
			t.Errorf("jsInt(%v) = %d, want %d", in, got, want)
		}
	}
}

func TestASEPlayerRawKeepsOnlyFlaggedFields(t *testing.T) {
	got := asePlayers([]ase.Player{{Flags: ase.FlagName | ase.FlagScore, Name: "a", Score: math.NaN(), Team: "ignored"}})
	if len(got) != 1 || got[0].Name != "a" || len(got[0].Raw) != 1 {
		t.Fatalf("players = %+v", got)
	}
	if s, ok := got[0].Raw["score"].(Number); !ok || !math.IsNaN(float64(s)) {
		t.Errorf("score = %#v, want Number(NaN)", got[0].Raw["score"])
	}
}
