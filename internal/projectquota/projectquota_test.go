package projectquota

import "testing"

func TestAllocate_IsDeterministicAndConservesEveryDimension(t *testing.T) {
	allocations, err := Allocate(Cap{CPU: "1", Memory: "10", Pods: 10}, []string{"zeta", "alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if got := allocations["alpha"]; got != (Cap{CPU: "334m", Memory: "4", Pods: 4}) {
		t.Fatalf("alpha allocation = %+v", got)
	}
	if got := allocations["beta"]; got != (Cap{CPU: "333m", Memory: "3", Pods: 3}) {
		t.Fatalf("beta allocation = %+v", got)
	}
	if got := allocations["zeta"]; got != (Cap{CPU: "333m", Memory: "3", Pods: 3}) {
		t.Fatalf("zeta allocation = %+v", got)
	}
}

func TestCapValidate(t *testing.T) {
	for _, cap := range []Cap{{CPU: "-1"}, {Memory: "nope"}, {Pods: -1}} {
		if err := cap.Validate(); err == nil {
			t.Fatalf("Validate(%+v) unexpectedly succeeded", cap)
		}
	}
	if err := (Cap{CPU: "500m", Memory: "1Gi", Pods: 1}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAllocate_FailsClosedWhenBoundedCapCannotCoverEveryNamespace(t *testing.T) {
	for _, cap := range []Cap{
		{CPU: "1m"},
		{Memory: "1"},
		{Pods: 1},
	} {
		if _, err := Allocate(cap, []string{"alpha", "beta"}); err == nil {
			t.Fatalf("Allocate(%+v) unexpectedly emitted an unbounded share", cap)
		}
	}
}
