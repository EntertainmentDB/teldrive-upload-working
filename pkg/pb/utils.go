package pb

import (
	"fmt"
	"math"
	"os"
)

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
	sizes := []string{" B", " kB", " MB", " GB", " TB", " PB", " EB"}
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
	val := math.Floor(float64(s)/math.Pow(base, e)*10+0.5) / 10
	f := "%.0f"
	if val < 10 {
		f = "%.1f"
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
