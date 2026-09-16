// Command vinylpreview prints a single frame of the record, for looking at it
// without the interface redrawing underneath the capture.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/Gaurav-Gosain/crate/internal/theme"
	"github.com/Gaurav-Gosain/crate/internal/ui"
)

func main() {
	rot := 0.8
	if len(os.Args) > 1 {
		if v, err := strconv.ParseFloat(os.Args[1], 64); err == nil {
			rot = v
		}
	}
	if len(os.Args) > 2 {
		theme.Set(os.Args[2])
	}
	for _, line := range ui.VinylPreview(40, 20, rot, theme.Current()) {
		fmt.Println(line)
	}
	bars := make([]float64, 60)
	for i := range bars {
		bars[i] = 0.25 + 0.7*float64((i*37)%11)/11
	}
	for _, line := range ui.BarsPreview(bars, 8, theme.Current()) {
		fmt.Println(line)
	}
}
