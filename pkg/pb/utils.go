package pb

import (
	"fmt"
	"math"
	"os"
	"time"
)

func calculateETA(rate, max, current float64) time.Duration {
	var eta time.Duration
	// timeLeft = time.Duration((max - current) / rate)
	eta = (time.Duration((1/rate)*(max-current)) * time.Second)

	if rate == 0 {
		eta = (time.Duration((max - current)) * time.Second)
	}
	if eta < 0 {
		return 0
	}
	return eta

}

func calculatePercent(current int, max int) int {
	percent := int((float64(current) / float64(max)) * 100)
	if percent < 0 {
		percent = 0
	}
	return percent
}

func average(xs []float64) float64 {
	total := 0.0
	for _, v := range xs {
		total += v
	}
	return total / float64(len(xs))
}

func logn(n, b float64) float64 {
	return math.Log(n) / math.Log(b)
}

func humanizeBytes(s float64, iec bool) (string, string) {
	sizes := []string{" B", " KB", " MB", " GB", " TB", " PB", " EB"}
	base := 1000.0

	if iec {
		sizes = []string{" B", " KiB", " MiB", " GiB", " TiB", " PiB", " EiB"}
		base = 1024.0
	}

	if s < 10 {
		return fmt.Sprintf("%2.0f", s), sizes[0]
	}
	e := math.Floor(logn(float64(s), base))
	suffix := sizes[int(e)]
	// you can use math.Round(...) instead of math.Floor(...+0.5)
	// rp is the rounding precision, change it to your need
	rp := 1000.0
	val := math.Floor(float64(s)/math.Pow(base, e)*rp+0.5) / rp
	f := "%.1f"
	if int(e) >= 3 {
		f = fmt.Sprintf("%%.%df", int(e)-1)
	}

	return fmt.Sprintf(f, val), suffix
}

func WriteToFile(path string, str string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintln(file, str)
	if err != nil {
		return err
	}
	return nil
}
