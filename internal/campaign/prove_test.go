package campaign

import (
	"context"
	"testing"
	"time"
)

func TestScenariosCatchTheBugTheyName(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, sc := range Scenarios() {
		report, err := Prove(ctx, sc)
		if err != nil {
			t.Fatalf("%s: %v", sc.Name, err)
		}
		if err := sc.Matches(report); err != nil {
			t.Fatal(err)
		}
	}
}
