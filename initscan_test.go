package scaner_test

import (
	"fmt"
	"testing"

	"github.com/tumarsal/scaner"
)

func TestScaner(t *testing.T) {
	scaner.TestArchiverZip()
	fmt.Printf("Done")
}
