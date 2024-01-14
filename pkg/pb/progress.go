package pb

import (
	"bytes"
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
	mu                   sync.Mutex
	progress             *Progress
	uploaded             int
	uploadedBytes        int64
	existing             int
	existingBytes        int64
	error                int
	errorBytes           int64
	totalAverageRate     float64
	totalTransfers       int
	totalSize            int64
	maxDescriptionLength int
	// error    int
}

type logWriter struct {
	progress *Progress
}

func (lw *logWriter) Write(b []byte) (n int, err error) {
	lw.progress.render(string(b))
	return len(b), nil
}

type Progress struct {
	Bars      []*Bar
	LogWriter *logWriter
	wg        *sync.WaitGroup
	config    progressConfig
	state     progressState
}

func NewProgress(wg *sync.WaitGroup, options ...ProgressOption) *Progress {
	p := Progress{wg: wg, config: progressConfig{
		writer:           configureOutputWriter(os.Stdout),
		throttleDuration: 65 * time.Millisecond,
	}}
	p.LogWriter = &logWriter{progress: &p}
	p.state.progress = &p

	for _, o := range options {
		o(&p)
	}
	return &p
}

func (p *Progress) StartProgress() func() {
	stopProgress := make(chan struct{})

	// oldLogPrint := fs.LogPrint

	// fs.LogPrint = func(level fs.LogLevel, text string) {
	// 	p.render(text)
	// }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(p.config.throttleDuration)

		for {
			select {
			case <-ticker.C:
				if err := p.render(""); err != nil {
					return
				}
			case <-stopProgress:
				ticker.Stop()
				// fs.LogPrint = oldLogPrint
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

func (p *Progress) updateMaxDescriptionLength() {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.maxDescriptionLength = 0
	for _, bar := range p.Bars {
		if !bar.IsCompleted() {
			sw := getStringWidth(&bar.config, bar.state.originalDescription, false)
			if sw > p.state.maxDescriptionLength {
				p.state.maxDescriptionLength = sw
			}
		}
	}
}

func (p *Progress) AddTransfer(totalFiles int, totalSize int64) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.totalSize += totalSize
	p.state.totalTransfers += totalFiles
}
func (p *Progress) AddExisting(size int64) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.existingBytes += size
	p.state.existing++
}
func (p *Progress) addError(size int64) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.errorBytes += size
	p.state.error++
}
func (p *Progress) addUploaded() {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.uploaded++
}
func (p *Progress) incrUploadedBytes(s int64) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.uploadedBytes += s
}
func (p *Progress) incrTotalAverage(s float64) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.totalAverageRate += s
}

var (
	nlines = 0 // number of lines in the previous stats block
)

func (p *Progress) render(logMessage string) error {
	strProgressBars, err := p.String()
	if err != nil {
		return err
	}
	strProgressStats := p.state.String()

	clearAndWriteProgress(&p.config, strProgressStats, strProgressBars, logMessage)

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

// OptionSetThrottle will wait the specified duration before updating again. The default
// duration is 65 milliseconds.
func OptionSetThrottle(duration time.Duration) ProgressOption {
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

func (p *Progress) resetState() {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.uploaded = 0
	p.state.totalAverageRate = 0
	p.state.uploadedBytes = 0
	p.state.error = 0
}

func (p *Progress) String() (string, error) {
	var bars strings.Builder

	p.resetState()
	p.updateMaxDescriptionLength()

	for i, bar := range p.Bars {
		updateProgressState(p, bar, &bars, i)
	}

	return bars.String(), nil
}

func updateProgressState(p *Progress, bar *Bar, bars *strings.Builder, index int) {
	if !bar.IsCompleted() {
		bar.Describe(truncateDescription(bar.state.originalDescription, p.state.maxDescriptionLength))
	}

	bar.mu.Lock()
	strBar, err := bar.getBar()
	bar.mu.Unlock()
	if err != nil {
		// Manejar el error de manera apropiada...
		return
	}

	if bar.IsError() {
		p.addError(bar.config.max)
		return
	}

	p.incrUploadedBytes(bar.state.currentBytes)

	if bar.IsCompleted() {
		p.addUploaded()
		return
	}

	bars.WriteString(strBar)
	if index != len(p.Bars)-1 && !bar.IsCompleted() {
		bars.WriteString("\n")
	}

	if !bar.IsFinished() {
		p.incrTotalAverage(bar.state.averageRate)
	}
}

func (ps *progressState) String() string {
	var strProgressStats strings.Builder
	p := ps.progress

	formatTransferredInfo := func() string {
		uploadedBytesHumanize, uploadedBytesSuffix := humanizeBytes(float64(p.state.uploadedBytes+p.state.existingBytes), false)
		totalSizeHumanize, totalSizeSuffix := humanizeBytes(float64(p.state.totalSize), false)
		speedHumanize, speedSuffix := humanizeBytes(p.state.totalAverageRate, false)

		return fmt.Sprintf("Transferred: %s, %s%s/s",
			fmt.Sprintf("%s%s/%s%s, %d%%", uploadedBytesHumanize, uploadedBytesSuffix, totalSizeHumanize, totalSizeSuffix, calculatePercent(int(p.state.uploadedBytes+p.state.existingBytes), int(p.state.totalSize))),
			speedHumanize, speedSuffix,
		)
	}

	formatProgressInfo := func() string {
		if p.state.totalTransfers != 0 {
			return fmt.Sprintf("Transferred: %d/%d, %d%%", p.state.uploaded+p.state.existing, p.state.totalTransfers, calculatePercent(p.state.uploaded+p.state.existing, p.state.totalTransfers))
		}
		return fmt.Sprintf("Transferred: %d/%d, %d%%", p.state.uploaded, p.state.totalTransfers, 0)
	}

	formatErrorInfo := func() string {
		if p.state.error > 0 {
			return fmt.Sprintf("Errors: %d\n", p.state.error)
		}
		return ""
	}

	strProgressStats.WriteString(formatTransferredInfo())
	strProgressStats.WriteString("\n")

	strProgressStats.WriteString(formatProgressInfo())
	strProgressStats.WriteString("\n")

	strProgressStats.WriteString(formatErrorInfo())

	strProgressStats.WriteString("Transferring:")

	return strProgressStats.String()
}

func clearAndWriteProgress(config *progressConfig, strProgressStats string, strProgressBars string, logMessage string) {
	var buf bytes.Buffer
	out := func(s string) {
		buf.WriteString(s)
	}
	if logMessage != "" {
		out("\n")
		out(MoveUp)
	}
	for i := 0; i < nlines-1; i++ {
		out(EraseLine)
		out(MoveUp)
	}
	out(EraseLine)
	out(MoveToStartOfLine)
	if logMessage != "" {
		out(EraseLine)
		out(logMessage + "\n")
	}

	lines := fmt.Sprintf("%s\n%s", strProgressStats, strProgressBars)
	fixedLines := strings.Split(lines, "\n")
	nlines = len(fixedLines)

	for i, line := range fixedLines {
		w, _ := termSize()
		lineWidth := getStringWidth(&barConfig{colorCodes: true}, line, true)
		if lineWidth > w {
			line = runewidth.Truncate(line, w, "...")
		}

		out(line)
		if i != nlines-1 {
			out("\n")
		}
	}
	writeToProgress(*config, buf.Bytes())
}
