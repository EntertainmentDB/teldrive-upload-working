package pb

import (
	"errors"
	"io"
	"sync"
	"time"
)

// Bar is a thread-safe, simple
// progress bar
type Bar struct {
	state  barState
	config barConfig
	lock   sync.Mutex
}

// String returns the current rendered version of the progress bar.
// It will never return an empty string while the progress bar is running.
func (b *Bar) String() string {
	return b.state.rendered
}

// RenderBlank renders the current bar state, you can use this to render a 0% state
func (b *Bar) RenderBlank() error {
	if b.config.invisible {
		return nil
	}
	if b.state.currentNum == 0 {
		b.state.lastShown = time.Time{}
	}
	// return b.getBarString()
	return nil
}

// Reset will reset the clock that is used
// to calculate current time and the time left.
func (b *Bar) Reset() {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.state = getBasicState()
}

// Finish will fill the bar to full
func (b *Bar) Finish() error {
	b.lock.Lock()
	b.state.currentNum = b.config.max
	b.lock.Unlock()
	return b.IncrInt(0)
}

// Exit will exit the bar to keep current state
func (b *Bar) Exit() error {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.state.exit = true
	if b.config.onCompletion != nil {
		b.config.onCompletion()
	}
	return nil
}

// IncrInt will add the specified amount to the progressbar
func (b *Bar) IncrInt(num int) error {
	return b.IncrInt64(int64(num))
}

// Set will set the bar to a current number
func (b *Bar) Set(num int) error {
	return b.Set64(int64(num))
}

// Set64 will set the bar to a current number
func (b *Bar) Set64(num int64) error {
	b.lock.Lock()
	toAdd := num - int64(b.state.currentBytes)
	b.lock.Unlock()
	return b.IncrInt64(toAdd)
}

// IncrInt64 will add the specified amount to the progressbar
func (b *Bar) IncrInt64(num int64) error {
	if b.config.invisible {
		return nil
	}
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.state.exit {
		return nil
	}

	// error out since OptionSpinnerCustom will always override a manually set spinnerType
	if b.config.spinnerTypeOptionUsed && len(b.config.spinner) > 0 {
		return errors.New("OptionSpinnerType and OptionSpinnerCustom cannot be used together")
	}

	if b.config.max == 0 {
		return errors.New("max must be greater than 0")
	}

	if b.state.currentNum < b.config.max {
		if b.config.ignoreLength {
			b.state.currentNum = (b.state.currentNum + num) % b.config.max
		} else {
			b.state.currentNum += num
		}
	}

	b.state.currentBytes += float64(num)

	// reset the countdown timer every second to take rolling average
	b.state.counterNumSinceLast += num
	if time.Since(b.state.counterTime).Seconds() > 0.5 {
		b.state.counterLastTenRates = append(b.state.counterLastTenRates, float64(b.state.counterNumSinceLast)/time.Since(b.state.counterTime).Seconds())
		if len(b.state.counterLastTenRates) > 10 {
			b.state.counterLastTenRates = b.state.counterLastTenRates[1:]
		}
		b.state.counterTime = time.Now()
		b.state.counterNumSinceLast = 0
	}

	percent := float64(b.state.currentNum) / float64(b.config.max)
	b.state.currentSaucerSize = int(percent * float64(b.config.width))
	b.state.currentPercent = int(percent * 100)

	b.state.lastPercent = b.state.currentPercent
	if b.state.currentNum > b.config.max {
		return errors.New("current number exceeds max")
	}

	// updateBar := b.state.currentPercent != b.state.lastPercent && b.state.currentPercent > 0
	// // always update if show bytes/second or its/second
	// if updateBar || b.config.showIterationsPerSecond || b.config.showIterationsCount {
	// 	return b.render()
	// }

	return nil
}

// Clear erases the progress bar from the current line
// func (b *Bar) Clear() error {
// 	return clearProgressBar(b.config, b.state)
// }

// Describe will change the description shown before the progress, which
// can be changed on the fly (as for a slow running process).
func (b *Bar) Describe(description string) {
	b.lock.Lock()
	defer b.lock.Unlock()
	b.state.description = description
	if b.config.invisible {
		return
	}
	b.getBar()
}

// GetMax returns the max of a bar
func (b *Bar) GetMax() int {
	return int(b.config.max)
}

// GetMax64 returns the current max
func (b *Bar) GetMax64() int64 {
	return b.config.max
}

// ChangeMax takes in a int
// and changes the max value
// of the progress bar
func (b *Bar) ChangeMax(newMax int) {
	b.ChangeMax64(int64(newMax))
}

// ChangeMax64 is basically
// the same as ChangeMax,
// but takes in a int64
// to avoid casting
func (b *Bar) ChangeMax64(newMax int64) {
	b.config.max = newMax

	if b.config.showBytes {
		b.config.maxHumanized, b.config.maxHumanizedSuffix = humanizeBytes(float64(b.config.max),
			b.config.useIECUnits)
	}

	b.IncrInt(0) // re-render
}

// IsFinished returns true if progress bar is finished
func (b *Bar) IsFinished() bool {
	return b.state.finished
}

// IsCompleted returns true if progress bar is completed
func (b *Bar) IsCompleted() bool {
	return b.state.completed
}

// getBar renders the progress bar, updating the maximum
// rendered line width. this function is not thread-safe,
// so it must be called with an acquired lock.
func (b *Bar) getBar() (string, error) {
	// make sure that the rendering is not happening too quickly
	// but always show if the currentNum reaches the max
	if time.Since(b.state.lastShown).Nanoseconds() < b.config.throttleDuration.Nanoseconds() &&
		b.state.currentNum < b.config.max {
		return "", nil
	}

	// if !b.config.useANSICodes {
	// 	// first, clear the existing progress bar
	// 	err := clearProgressBar(b.config, b.state)
	// 	if err != nil {
	// 		return "", err
	// 	}
	// }

	// check if the progress bar is finished
	if !b.state.finished && b.state.currentNum >= b.config.max {
		b.state.finished = true
		if !b.config.clearOnFinish {
			getBarString(&b.config, &b.state)
		}
		if b.config.onCompletion != nil {
			b.config.onCompletion()
		}
	}
	if b.IsCompleted() {
		// when using ANSI codes we don't pre-clean the current line
		// if b.config.useANSICodes && b.config.clearOnFinish {
		// 	err := clearProgressBar(b.config, b.state)
		// 	if err != nil {
		// 		return "", err
		// 	}
		// }
		return "", nil
	}

	// then, re-render the current progress bar
	w, strBar, err := getBarString(&b.config, &b.state)
	if err != nil {
		return "", err
	}

	if w > b.state.maxLineWidth {
		b.state.maxLineWidth = w
	}

	b.state.lastShown = time.Now()

	return strBar, nil
}

// State returns the current state
func (b *Bar) State() BarState {
	b.lock.Lock()
	defer b.lock.Unlock()
	s := BarState{}
	s.CurrentPercent = float64(b.state.currentNum) / float64(b.config.max)
	s.CurrentBytes = b.state.currentBytes
	s.SecondsSince = time.Since(b.state.startTime).Seconds()
	if b.state.currentNum > 0 {
		s.SecondsLeft = s.SecondsSince / float64(b.state.currentNum) * (float64(b.config.max) - float64(b.state.currentNum))
	}
	s.KBsPerSecond = float64(b.state.currentBytes) / 1024.0 / s.SecondsSince
	return s
}

// Write implement io.Writer
func (b *Bar) Write(byte []byte) (n int, err error) {
	n = len(byte)
	b.IncrInt(n)
	return
}

// Read implement io.Reader
func (b *Bar) Read(byte []byte) (n int, err error) {
	n = len(byte)
	b.IncrInt(n)
	return
}

// Close close the bar forever
func (b *Bar) Close() (err error) {
	b.lock.Lock()
	b.state.completed = true
	b.lock.Unlock()
	return
}

// Reader is the progressbar io.Reader struct
type Reader struct {
	io.Reader
	bar *Bar
}

// NewReader return a new Reader with a given progress bar.
func NewReader(r io.Reader, bar *Bar) Reader {
	return Reader{
		Reader: r,
		bar:    bar,
	}
}

// Read will read the data and add the number of bytes to the progressbar
func (r *Reader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	r.bar.IncrInt(n)
	return
}

// Close the reader when it implements io.Closer
func (r *Reader) Close() (err error) {
	if closer, ok := r.Reader.(io.Closer); ok {
		return closer.Close()
	}
	r.bar.Finish()
	return
}
