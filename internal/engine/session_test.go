package engine

import (
	"context"
	"testing"
)

func TestGit(t *testing.T) {
	g, v, err := Git(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil || v.Major < 2 {
		t.Fatalf("runner %v, version %v", g, v)
	}
}
