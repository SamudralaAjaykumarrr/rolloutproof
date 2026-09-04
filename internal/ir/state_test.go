package ir

import "testing"

func TestNewRolloutState_DeduplicatesAndSortsLiveVersions(t *testing.T) {
	s := NewRolloutState(0, []LiveVersion{
		{ServiceName: "api", Version: "v2"},
		{ServiceName: "api", Version: "v1"},
		{ServiceName: "api", Version: "v1"},
	}, SchemaState{})
	if len(s.Live) != 2 {
		t.Fatalf("expected deduplication to leave 2 entries, got %d: %+v", len(s.Live), s.Live)
	}
	if s.Live[0].Version != "v1" || s.Live[1].Version != "v2" {
		t.Fatalf("expected sorted order v1, v2, got %+v", s.Live)
	}
}

func TestRolloutState_LiveVersionsFor(t *testing.T) {
	s := NewRolloutState(0, []LiveVersion{
		{ServiceName: "api", Version: "v1"},
		{ServiceName: "api", Version: "v2"},
		{ServiceName: "payments", Version: "v3"},
	}, SchemaState{})
	got := s.LiveVersionsFor("api")
	if len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got := s.LiveVersionsFor("unknown-service"); len(got) != 0 {
		t.Fatalf("expected empty slice for a service with no live versions, got %+v", got)
	}
}

func TestVerdict_ZeroValueIsUnknown(t *testing.T) {
	var v Verdict
	if v != VerdictUnknown {
		t.Fatalf("expected zero value of Verdict to be VerdictUnknown, got %v", v)
	}
}

func TestVerdict_CombineDominanceOrder(t *testing.T) {
	cases := []struct {
		a, b, want Verdict
	}{
		{VerdictSafe, VerdictSafe, VerdictSafe},
		{VerdictSafe, VerdictUnknown, VerdictUnknown},
		{VerdictUnknown, VerdictSafe, VerdictUnknown},
		{VerdictSafe, VerdictUnsafe, VerdictUnsafe},
		{VerdictUnknown, VerdictUnsafe, VerdictUnsafe},
		{VerdictUnsafe, VerdictUnsafe, VerdictUnsafe},
		{VerdictUnknown, VerdictUnknown, VerdictUnknown},
	}
	for _, c := range cases {
		if got := Combine(c.a, c.b); got != c.want {
			t.Fatalf("Combine(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestRollbackVerdict_ZeroValueIsUnknown(t *testing.T) {
	var r RollbackVerdict
	if r != RollbackUnknown {
		t.Fatalf("expected zero value of RollbackVerdict to be RollbackUnknown, got %v", r)
	}
}
