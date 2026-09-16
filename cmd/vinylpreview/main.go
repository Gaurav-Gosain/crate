// Command vinylpreview draws one frame of the record and the visualiser, for
// looking at them without the interface repainting underneath the capture.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/player"
	"github.com/Gaurav-Gosain/crate/internal/theme"
	"github.com/Gaurav-Gosain/crate/internal/ui"
)

func main() {
	src := ""
	if len(os.Args) > 1 {
		src = os.Args[1]
	}
	at := 30.0
	if len(os.Args) > 2 {
		at, _ = strconv.ParseFloat(os.Args[2], 64)
	}
	if len(os.Args) > 3 {
		if err := theme.Set(os.Args[3]); err != nil {
			fmt.Fprintf(os.Stderr, "vinylpreview: %v\n", err)
		}
	}
	t := theme.Current()

	for _, line := range ui.VinylPreview(34, 17, 0.8, t) {
		fmt.Println(line)
	}
	fmt.Println()

	if src == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sp := player.Analyse(ctx, src)
	for i := 0; i < 200 && !sp.Ready(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if err := sp.Err(); err != nil {
		fmt.Println("decode:", err)
		return
	}
	pos := time.Duration(at * float64(time.Second))
	// Advance a few frames so the smoothing settles, as it would in use.
	var vals, peaks []float64
	for i := range 20 {
		vals, peaks = sp.BarsWithPeaks(pos+time.Duration(i)*40*time.Millisecond, ui.BarCount(100))
	}
	for _, line := range ui.BarsPreview(vals, peaks, 12, t) {
		fmt.Println(line)
	}
}
