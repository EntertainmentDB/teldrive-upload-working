package pb

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

type progressConfig struct {
	writer           io.Writer
	throttleDuration time.Duration
}

type progressState struct {
	uploaded             int
	uploadedBytes        float64
	existing             int
	existingBytes        float64
	totalAverageRate     float64
	totalTransfers       int
	totalSize            int64
	maxDescriptionLength int
	// error    int
}

type Progress struct {
	Bars   []*Bar
	lock   sync.Mutex
	wg     *sync.WaitGroup
	config progressConfig
	state  progressState
}

func NewProgress(wg *sync.WaitGroup, options ...ProgressOption) *Progress {
	p := Progress{wg: wg, config: progressConfig{
		writer:           configureOutputWriter(os.Stdout),
		throttleDuration: 65 * time.Millisecond,
	}}
	for _, o := range options {
		o(&p)
	}
	return &p
}

func (p *Progress) StartProgress() func() {
	stopProgress := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(p.config.throttleDuration)

		for {
			select {
			case <-ticker.C:
				if err := p.render(); err != nil {
					return
				}
			case <-stopProgress:
				ticker.Stop()
				fmt.Println("")
				return
			}
		}
	}()

	return func() {
		time.Sleep(1000 * time.Millisecond)
		close(stopProgress)
		wg.Wait()
	}
}

func (p *Progress) AddBar(newBar *Bar) {
	p.Bars = append(p.Bars, newBar)
}

func (p *Progress) Wait() {
	p.wg.Wait()
}

func (p *Progress) AddTransfer(totalFiles int, totalSize int64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state.totalSize += totalSize
	p.state.totalTransfers += totalFiles
}
func (p *Progress) AddExisting(size float64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state.existingBytes += size
	p.state.existing++
}

var (
	nlines = 0 // number of lines in the previous stats block
)

func (p *Progress) render() error {
	strProgressBars, err := generateProgressBars(p)
	if err != nil {
		return err
	}
	strProgressStats := generateProgressStats(p)

	clearAndWriteProgress(&p.config, strProgressStats, strProgressBars)

	return nil
}

// ProgressOption is the type all options need to adhere to
type ProgressOption func(p *Progress)

// OptionSetWriter sets the output writer (defaults to os.StdOut)
func OptionSetWriter(w io.Writer) ProgressOption {
	return func(p *Progress) {
		p.config.writer = configureOutputWriter(w)
	}
}

// OptionThrottle will wait the specified duration before updating again. The default
// duration is 0 seconds.
func OptionThrottle(duration time.Duration) ProgressOption {
	return func(p *Progress) {
		p.config.throttleDuration = duration
	}
}

func configureOutputWriter(w io.Writer) io.Writer {
	writer := w

	if file, ok := w.(*os.File); ok {
		if !term.IsTerminal(int(file.Fd())) {
			// If stdout is not a tty, remove escape codes
			writer = colorable.NewNonColorable(w)
		} else {
			writer = colorable.NewColorable(w.(*os.File))
		}
	}

	return writer
}

func truncateDescription(description string, length int) string {
	const maxDescriptionLength = 59
	if length > maxDescriptionLength {
		length = maxDescriptionLength
	}

	w, _ := termSize()
	if length > w/2 {
		length = w / 2
	}

	if length%2 == 0 {
		length--
	}

	descLength := runewidth.StringWidth(description)

	if descLength > length {
		half := (length - 3) / 2
		return runewidth.Truncate(description, half, "") + "..." + runewidth.TruncateLeft(description, descLength-half, "")
	} else {
		return runewidth.FillLeft(description, length)
	}
}

func generateProgressBars(p *Progress) (string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	var strProgressBars strings.Builder

	p.state.uploaded = 0
	p.state.totalAverageRate = 0
	p.state.uploadedBytes = 0
	p.state.maxDescriptionLength = 0

	for _, bar := range p.Bars {
		if !bar.IsCompleted() {
			sw := getStringWidth(&bar.config, bar.state.originalDescription, false)
			if sw > p.state.maxDescriptionLength {
				p.state.maxDescriptionLength = sw
			}
		}
	}

	for i, bar := range p.Bars {
		if !bar.IsCompleted() {
			bar.Describe(truncateDescription(bar.state.originalDescription, p.state.maxDescriptionLength))
		}

		strBar, err := bar.getBar()
		if err != nil {
			return "", err
		}
		p.state.uploadedBytes += bar.state.currentBytes

		if bar.IsCompleted() {
			p.state.uploaded++
			continue
		}

		strProgressBars.WriteString(strBar)
		if i != len(p.Bars)-1 && !bar.IsCompleted() {
			strProgressBars.WriteString("\n")
		}

		if !bar.IsFinished() {
			p.state.totalAverageRate += bar.state.averageRate
		}
	}

	return strProgressBars.String(), nil
}

func generateProgressStats(p *Progress) string {
	var strProgressStats strings.Builder

	uploadedBytesHumanize, uploadedBytesSuffix := humanizeBytes(float64(p.state.uploadedBytes)+p.state.existingBytes, false)
	totalSizeHumanize, totalSizeSuffix := humanizeBytes(float64(p.state.totalSize), false)
	speedHumanize, speedSuffix := humanizeBytes(p.state.totalAverageRate, false)

	transferredInfo := fmt.Sprintf("Transferred: %s, %s",
		fmt.Sprintf("%s%s/%s%s, %d%%", uploadedBytesHumanize, uploadedBytesSuffix, totalSizeHumanize, totalSizeSuffix, calculatePercent(int(p.state.uploadedBytes), int(p.state.totalSize))),
		fmt.Sprintf("%s%s/s", speedHumanize, speedSuffix),
	)
	strProgressStats.WriteString(transferredInfo)
	strProgressStats.WriteString("\n")

	progressInfo := ""
	if p.state.totalTransfers != 0 {
		progressInfo = fmt.Sprintf("Transferred: %d/%d, %d%%", p.state.uploaded+p.state.existing, p.state.totalTransfers, calculatePercent(p.state.uploaded, p.state.totalTransfers))
	} else {
		progressInfo = fmt.Sprintf("Transferred: %d/%d, %d%%", p.state.uploaded, p.state.totalTransfers, 0)
	}
	strProgressStats.WriteString(progressInfo)
	strProgressStats.WriteString("\n")

	strProgressStats.WriteString("Transferring:")

	return strProgressStats.String()
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

func clearAndWriteProgress(config *progressConfig, strProgressStats string, strProgressBars string) {
	for i := 0; i < nlines-1; i++ {
		writeStringToProgress(*config, EraseLine)
		writeStringToProgress(*config, MoveUp)
	}
	writeStringToProgress(*config, EraseLine)
	writeStringToProgress(*config, MoveToStartOfLine)

	lines := fmt.Sprintf("%s\n%s", strProgressStats, strProgressBars)
	fixedLines := strings.Split(lines, "\n")
	nlines = len(fixedLines)

	for i, line := range fixedLines {
		w, _ := termSize()
		lineWidth := getStringWidth(&barConfig{colorCodes: true}, line, true)
		if lineWidth > w {
			line = runewidth.Truncate(line, w, "...")
		}

		writeStringToProgress(*config, line)
		if i != nlines-1 {
			writeStringToProgress(*config, "\n")
		}
	}
}

// func clearProgressBars(config progressConfig, lines int) {
// 	for i := 0; i < lines; i++ {
// 		writeString(config, EraseLine)
// 		writeString(config, MoveUp)
// 	}
// 	writeString(config, EraseLine)
// 	writeString(config, MoveToStartOfLine)
// }

// func clearProgressBar(c barConfig, s barState) error {
// 	if s.maxLineWidth == 0 {
// 		return nil
// 	}
// 	if c.useANSICodes {
// 		// write the "clear current line" ANSI escape sequence
// 		return writeString(c, "\033[2K\r")
// 	}
// 	// fill the empty content
// 	// to overwrite the progress bar and jump
// 	// back to the beginning of the line
// 	str := fmt.Sprintf("\r%s\r", strings.Repeat(" ", s.maxLineWidth))
// 	return writeString(c, str)
// 	// the following does not show correctly if the previous line is longer than subsequent line
// 	// return writeString(c, "\r")
// }
