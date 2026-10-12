package run

import (
	"strconv"
	"strings"
	"testing"
)

func TestMeasurementEffortAdmission(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	for _, value := range []string{"1ns", "1s", "+1s", "0.1s", "1x", "01x", "+1x", strconv.Itoa(maximum) + "x"} {
		if err := (Options{Count: 1, Benchtime: value}).ValidateEffort(); err != nil {
			t.Fatalf("valid %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "0s", "-1ns", "0x", "-1x", "1.5x", "NaN", strconv.FormatUint(uint64(maximum)+1, 10) + "x", strings.Repeat("9", 40) + "h"} {
		if err := (Options{Count: 1, Benchtime: value}).ValidateEffort(); err == nil {
			t.Fatalf("invalid budget %q admitted", value)
		}
	}
	for _, count := range []int{0, -1} {
		if err := (Options{Count: count, Benchtime: "1x"}).ValidateEffort(); err == nil {
			t.Fatalf("count %d admitted", count)
		}
	}
}
