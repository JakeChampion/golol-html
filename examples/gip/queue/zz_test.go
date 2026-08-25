package main

import (
	"fmt"
	"testing"
)

func TestZZ(t *testing.T) {
	items := makeItems(30, 128)
	for _, w := range []int{1, 1, 1, 4, 4, 4, 8, 2} {
		out, _ := Run(items, w, 50)
		fmt.Printf("workers=%d build=%.0f full=%.0f\n", w, out.AllocsBuild, out.AllocsFull)
	}
}
