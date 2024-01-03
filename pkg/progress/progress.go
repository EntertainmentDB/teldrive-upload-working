package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"golang.org/x/term"
)

type progressConfig struct {
	writer io.Writer
}

type Progress struct {
	Bars   []*Bar
	wg     *sync.WaitGroup
	config progressConfig
}

func NewProgress(wg *sync.WaitGroup, options ...ProgressOption) *Progress {
	p := Progress{wg: wg, config: progressConfig{
		writer: configureOutputWriter(os.Stdout),
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
		ticker := time.NewTicker(1000 * time.Millisecond)

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

var (
	nlines = 0 // number of lines in the previous stats block
)

func (p *Progress) render() error {
	var strProgress strings.Builder

	for _, bar := range p.Bars {
		// updateBar := bar.state.currentPercent != bar.state.lastPercent && bar.state.currentPercent > 0
		// // always update if show bytes/second or its/second
		// if updateBar || bar.config.showIterationsPerSecond || bar.config.showIterationsCount {

		// }
		strBar, err := bar.getBar()
		if err != nil {
			return err
		}
		if bar.state.completed {
			continue
		}
		strProgress.WriteString("\n")
		strProgress.WriteString(strBar)

		// if bar.IsFinished() {
		// 	return nil
		// }
	}
	clearProgressBars(p.config, nlines-1)
	fixedLines := strings.Split(strProgress.String(), "\n")
	nlines = len(fixedLines)

	for i, line := range fixedLines {
		// w, _ := termSize()
		writeString(p.config, line)
		if i != nlines-1 {
			writeString(p.config, "\n")
		}
	}

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

func clearProgressBars(config progressConfig, lines int) {
	for i := 0; i < lines; i++ {
		writeString(config, EraseLine)
		writeString(config, MoveUp)
	}
	writeString(config, EraseLine)
	writeString(config, MoveToStartOfLine)
}

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
