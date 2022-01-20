package util

import (
	"fmt"
)

func ExampleProduceMapReduce() {
	input := []int{0, 1, 2, 3}
	inputCh := make(chan int)
	errCh := make(chan error)
	produce := func() error {
		defer close(inputCh)
		for _, x := range input {
			inputCh <- x
		}
		return nil
	}
	outputCh := make(chan int)
	map_ := func() error {
		for x := range inputCh {
			if x == 2 {
				errCh <- fmt.Errorf("%d", x)
			} else {
				outputCh <- x
			}
		}
		return nil
	}
	done := func() { close(outputCh) }
	reduce := func() error {
		r := 0
		for x := range outputCh {
			r += x
		}
		fmt.Println(r)
		return nil
	}
	if err := ProduceMapReduce(0, produce, map_, reduce, done, errCh); err != nil {
		fmt.Println("error:", err)
	}
	// Output:
	// 4
	// error: 2
}
