package main

import (
	"testing"
)

// TestBlockTree_AppendBlock is a unit test.
func TestBlockTree_AppendBlock(t *testing.T) {
	bt := NewBlockTree()
	bt.AppendBlockByNumber(genesisBlock)
	if len(bt[0]) == 0 {
		t.Fatal("missing genesis at index=0")
	}
	bt.AppendBlockByNumber(&Block{
		i: 1,
	})
	if len(bt[1]) == 0 {
		t.Fatal("missing block i=1 at index=1")
	}
}
