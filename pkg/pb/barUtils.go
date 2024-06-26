package pb

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/mitchellh/colorstring"
	"golang.org/x/term"
)

// BarState is the basic properties of the bar
type BarState struct {
	CurrentPercent float64
	CurrentBytes   float64
	SecondsSince   float64
	SecondsLeft    float64
	KBsPerSecond   float64
}

type barState struct {
	originalDescription string
	description         string

	currentNum        int64
	currentPercent    int
	lastPercent       int
	currentSaucerSize int
	isAltSaucerHead   bool

	lastShown time.Time
	startTime time.Time

	counterTime         time.Time
	counterNumSinceLast int64
	counterLastTenRates []float64
	averageRate         float64

	maxLineWidth int
	currentBytes int64

	completed bool
	finished  bool
	exit      bool // Progress bar exit halfway
	error     bool

	rendered string
}

type barConfig struct {
	max                int64 // max number of the counter
	maxHumanized       string
	maxHumanizedSuffix string
	width              int
	// writer               io.Writer
	theme                Theme
	renderWithBlankState bool
	iterationString      string
	ignoreLength         bool // ignoreLength if max bytes not known

	// whether the output is expected to contain color codes
	colorCodes bool

	// show rate of change in kB/sec or MB/sec
	showBytes bool
	// show the iterations per second
	showIterationsPerSecond bool
	showIterationsCount     bool

	// whether the progress bar should show elapsed time.
	// always enabled if predictTime is true.
	elapsedTime bool

	showElapsedTimeOnFinish bool

	// whether the progress bar should attempt to predict the finishing
	// time of the progress based on the start time and the average
	// number of seconds between  increments.
	predictTime bool

	// minimum time to wait in between updates
	throttleDuration time.Duration

	// clear bar once finished
	clearOnFinish bool

	// spinnerType should be a number between 0-75
	spinnerType int

	// spinnerTypeOptionUsed remembers if the spinnerType was changed manually
	spinnerTypeOptionUsed bool

	// spinner represents the spinner as a slice of string
	spinner []string

	// fullWidth specifies whether to measure and set the bar to a specific width
	fullWidth bool

	// invisible doesn't render the bar at all, useful for debugging
	invisible bool

	onCompletion func()

	// whether the render function should make use of ANSI codes to reduce console I/O
	useANSICodes bool

	// whether to use the IEC units (e.g. MiB) instead of the default SI units (e.g. MB)
	useIECUnits bool

	// showDescriptionAtLineEnd specifies whether description should be written at line end instead of line start
	showDescriptionAtLineEnd bool
}

// Theme defines the elements of the bar
type Theme struct {
	Saucer        string
	AltSaucerHead string
	SaucerHead    string
	SaucerPadding string
	BarStart      string
	BarEnd        string
}

// BarOption is the type all options need to adhere to
type BarOption func(p *Bar)

// OptionSetWidth sets the width of the bar
func OptionSetWidth(s int) BarOption {
	return func(p *Bar) {
		p.config.width = s
	}
}

// OptionSpinnerType sets the type of spinner used for indeterminate bars
func OptionSpinnerType(spinnerType int) BarOption {
	return func(p *Bar) {
		p.config.spinnerTypeOptionUsed = true
		p.config.spinnerType = spinnerType
	}
}

// OptionSpinnerCustom sets the spinner used for indeterminate bars to the passed
// slice of string
func OptionSpinnerCustom(spinner []string) BarOption {
	return func(p *Bar) {
		p.config.spinner = spinner
	}
}

// OptionSetTheme sets the elements the bar is constructed of
func OptionSetTheme(t Theme) BarOption {
	return func(p *Bar) {
		p.config.theme = t
	}
}

// OptionSetVisibility sets the visibility
func OptionSetVisibility(visibility bool) BarOption {
	return func(p *Bar) {
		p.config.invisible = !visibility
	}
}

// OptionFullWidth sets the bar to be full width
func OptionFullWidth() BarOption {
	return func(p *Bar) {
		p.config.fullWidth = true
	}
}

// OptionSetRenderBlankState sets whether or not to render a 0% bar on construction
func OptionSetRenderBlankState(r bool) BarOption {
	return func(p *Bar) {
		p.config.renderWithBlankState = r
	}
}

// OptionSetDescription sets the description of the bar to render in front of it
func OptionSetDescription(description string) BarOption {
	return func(p *Bar) {
		p.state.description = description
		p.state.originalDescription = description
	}
}

// OptionEnableColorCodes enables or disables support for color codes
// using mitchellh/colorstring
func OptionEnableColorCodes(colorCodes bool) BarOption {
	return func(p *Bar) {
		p.config.colorCodes = colorCodes
	}
}

// OptionSetElapsedTime will enable elapsed time. Always enabled if OptionSetPredictTime is true.
func OptionSetElapsedTime(elapsedTime bool) BarOption {
	return func(p *Bar) {
		p.config.elapsedTime = elapsedTime
	}
}

// OptionSetPredictTime will also attempt to predict the time remaining.
func OptionSetPredictTime(predictTime bool) BarOption {
	return func(p *Bar) {
		p.config.predictTime = predictTime
	}
}

// OptionShowCount will also print current count out of total
func OptionShowCount() BarOption {
	return func(p *Bar) {
		p.config.showIterationsCount = true
	}
}

// OptionShowIts will also print the iterations/second
func OptionShowIts() BarOption {
	return func(p *Bar) {
		p.config.showIterationsPerSecond = true
	}
}

// OptionShowElapsedOnFinish will keep the display of elapsed time on finish
func OptionShowElapsedTimeOnFinish() BarOption {
	return func(p *Bar) {
		p.config.showElapsedTimeOnFinish = true
	}
}

// OptionSetItsString sets what's displayed for iterations a second. The default is "it" which would display: "it/s"
func OptionSetItsString(iterationString string) BarOption {
	return func(p *Bar) {
		p.config.iterationString = iterationString
	}
}

// OptionClearOnFinish will clear the bar once its finished
func OptionClearOnFinish() BarOption {
	return func(p *Bar) {
		p.config.clearOnFinish = true
	}
}

// OptionOnCompletion will invoke cmpl function once its finished
func OptionOnCompletion(cmpl func()) BarOption {
	return func(p *Bar) {
		p.config.onCompletion = cmpl
	}
}

// OptionShowBytes will update the progress bar
// configuration settings to display/hide kBytes/Sec
func OptionShowBytes(val bool) BarOption {
	return func(p *Bar) {
		p.config.showBytes = val
	}
}

// OptionUseANSICodes will use more optimized terminal i/o.
//
// Only useful in environments with support for ANSI escape sequences.
func OptionUseANSICodes(val bool) BarOption {
	return func(p *Bar) {
		p.config.useANSICodes = val
	}
}

// OptionUseIECUnits will enable IEC units (e.g. MiB) instead of the default
// SI units (e.g. MB).
func OptionUseIECUnits(val bool) BarOption {
	return func(p *Bar) {
		p.config.useIECUnits = val
	}
}

// OptionShowDescriptionAtLineEnd defines whether description should be written at line end instead of line start
func OptionShowDescriptionAtLineEnd() BarOption {
	return func(p *Bar) {
		p.config.showDescriptionAtLineEnd = true
	}
}

var defaultTheme = Theme{Saucer: "█", SaucerPadding: " ", BarStart: "|", BarEnd: "|"}

// NewOptions constructs a new instance of Bar, with any options you specify
func NewOptions(max int, options ...BarOption) *Bar {
	return NewOptions64(int64(max), options...)
}

// NewOptions64 constructs a new instance of Bar, with any options you specify
func NewOptions64(max int64, options ...BarOption) *Bar {
	b := Bar{
		state: getBasicState(),
		config: barConfig{
			// writer:           os.Stdout,
			theme:            defaultTheme,
			iterationString:  "it",
			width:            40,
			max:              max,
			throttleDuration: 0 * time.Nanosecond,
			elapsedTime:      true,
			predictTime:      true,
			spinnerType:      9,
			invisible:        false,
		},
	}

	for _, o := range options {
		o(&b)
	}

	if b.config.spinnerType < 0 || b.config.spinnerType > 75 {
		panic("invalid spinner type, must be between 0 and 75")
	}

	// ignoreLength if max bytes not known
	if b.config.max == -1 {
		b.config.ignoreLength = true
		b.config.max = int64(b.config.width)
		b.config.predictTime = false
	}

	b.config.maxHumanized, b.config.maxHumanizedSuffix = humanizeBytes(float64(b.config.max),
		b.config.useIECUnits)

	if b.config.renderWithBlankState {
		b.RenderBlank()
	}

	return &b
}

func getBasicState() barState {
	now := time.Now()
	return barState{
		startTime:   now,
		lastShown:   now,
		counterTime: now,
	}
}

// New returns a new Bar
// with the specified maximum
func New(max int) *Bar {
	return NewOptions(max)
}

// DefaultBytes provides a progressbar to measure byte
// throughput with recommended defaults.
// Set maxBytes to -1 to use as a spinner.
func DefaultBytes(maxBytes int64, description ...string) *Bar {
	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}
	return NewOptions64(
		maxBytes,
		OptionSetDescription(desc),
		// OptionSetWriter(os.Stderr),
		OptionShowBytes(true),
		OptionSetWidth(10),
		OptionShowCount(),
		OptionOnCompletion(func() {
			fmt.Fprint(os.Stderr, "\n")
		}),
		OptionSpinnerType(14),
		OptionFullWidth(),
		OptionSetRenderBlankState(true),
	)
}

// DefaultBytesSilent is the same as DefaultBytes, but does not output anywhere.
// String() can be used to get the output instead.
func DefaultBytesSilent(maxBytes int64, description ...string) *Bar {
	// Mostly the same bar as DefaultBytes

	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}
	return NewOptions64(
		maxBytes,
		OptionSetDescription(desc),
		// OptionSetWriter(io.Discard),
		OptionShowBytes(true),
		OptionSetWidth(10),
		OptionShowCount(),
		OptionSpinnerType(14),
		OptionFullWidth(),
	)
}

// Default provides a progressbar with recommended defaults.
// Set max to -1 to use as a spinner.
func Default(max int64, description ...string) *Bar {
	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}
	return NewOptions64(
		max,
		OptionSetDescription(desc),
		// OptionSetWriter(os.Stderr),
		OptionSetWidth(10),
		OptionShowCount(),
		OptionShowIts(),
		OptionOnCompletion(func() {
			fmt.Fprint(os.Stderr, "\n")
		}),
		OptionSpinnerType(14),
		OptionFullWidth(),
		OptionSetRenderBlankState(true),
	)
}

// DefaultSilent is the same as Default, but does not output anywhere.
// String() can be used to get the output instead.
func DefaultSilent(max int64, description ...string) *Bar {
	// Mostly the same bar as Default

	desc := ""
	if len(description) > 0 {
		desc = description[0]
	}
	return NewOptions64(
		max,
		OptionSetDescription(desc),
		// OptionSetWriter(io.Discard),
		OptionSetWidth(10),
		OptionShowCount(),
		OptionShowIts(),
		OptionSpinnerType(14),
		OptionFullWidth(),
	)
}

// New64 returns a new Bar
// with the specified maximum
func New64(max int64) *Bar {
	return NewOptions64(max)
}

// regex matching ansi escape codes
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func getStringWidth(c *barConfig, str string, colorize bool) int {
	if c != nil {
		if c.colorCodes {
			// convert any color codes in the progress bar into the respective ANSI codes
			str = colorstring.Color(str)
		}
	}

	// the width of the string, if printed to the console
	// does not include the carriage return character
	cleanString := strings.Replace(str, "\r", "", -1)

	if c != nil {
		if c.colorCodes {
			// the ANSI codes for the colors do not take up space in the console output,
			// so they do not count towards the output string width
			cleanString = ansiRegex.ReplaceAllString(cleanString, "")
		}
	}

	return runewidth.StringWidth(cleanString)
}

func getBarString(c *barConfig, s *barState) (int, string, error) {
	var sb strings.Builder

	s.averageRate = average(s.counterLastTenRates)
	if len(s.counterLastTenRates) == 0 || s.finished {
		// if no average samples, or if finished,
		// then average rate should be the total rate
		if t := time.Since(s.startTime).Seconds(); t > 0 {
			s.averageRate = float64(s.currentBytes) / t
		} else {
			s.averageRate = 0
		}
	}

	// show iteration count in "current/total" iterations format
	if c.showIterationsCount {
		if sb.Len() == 0 {
			sb.WriteString("(")
		} else {
			sb.WriteString(", ")
		}
		if !c.ignoreLength {
			if c.showBytes {
				currentHumanize, currentSuffix := humanizeBytes(float64(s.currentBytes), c.useIECUnits)
				if currentSuffix == c.maxHumanizedSuffix {
					sb.WriteString(fmt.Sprintf("%s/%s%s",
						currentHumanize, c.maxHumanized, c.maxHumanizedSuffix))
				} else {
					sb.WriteString(fmt.Sprintf("%s%s/%s%s",
						currentHumanize, currentSuffix, c.maxHumanized, c.maxHumanizedSuffix))
				}
			} else {
				sb.WriteString(fmt.Sprintf("%d/%d", s.currentBytes, c.max))
			}
		} else {
			if c.showBytes {
				currentHumanize, currentSuffix := humanizeBytes(float64(s.currentBytes), c.useIECUnits)
				sb.WriteString(fmt.Sprintf("%s%s", currentHumanize, currentSuffix))
			} else {
				sb.WriteString(fmt.Sprintf("%d/%s", s.currentBytes, "-"))
			}
		}
	}

	if !s.finished {
		// show rolling average rate
		if c.showBytes && s.averageRate > 0 && !math.IsInf(s.averageRate, 1) {
			if sb.Len() == 0 {
				sb.WriteString("(")
			} else {
				sb.WriteString(", ")
			}
			currentHumanize, currentSuffix := humanizeBytes(s.averageRate, c.useIECUnits)
			sb.WriteString(fmt.Sprintf("%s%s/s", currentHumanize, currentSuffix))
		}

		// show iterations rate
		if c.showIterationsPerSecond {
			if sb.Len() == 0 {
				sb.WriteString("(")
			} else {
				sb.WriteString(", ")
			}
			if s.averageRate > 1 {
				sb.WriteString(fmt.Sprintf("%0.0f %s/s", s.averageRate, c.iterationString))
			} else if s.averageRate*60 > 1 {
				sb.WriteString(fmt.Sprintf("%0.0f %s/min", 60*s.averageRate, c.iterationString))
			} else {
				sb.WriteString(fmt.Sprintf("%0.0f %s/hr", 3600*s.averageRate, c.iterationString))
			}
		}
	}

	if sb.Len() > 0 {
		sb.WriteString(")")
	}

	leftBrac, rightBrac, saucer, saucerHead := "", "", "", ""

	// show time prediction in "current/total" seconds format
	switch {
	case c.predictTime:
		rightBrac = calculateETA(s.averageRate, float64(c.max), float64(s.currentNum)).String()
		fallthrough
	case c.elapsedTime:
		leftBrac = (time.Duration(time.Since(s.startTime).Seconds()) * time.Second).String()
	}

	if c.fullWidth && !c.ignoreLength {
		width, _ := termSize()

		amend := 1 // an extra space at eol
		switch {
		case leftBrac != "" && rightBrac != "":
			amend = 4 // space, square brackets and colon
		case leftBrac != "" && rightBrac == "":
			amend = 4 // space and square brackets and another space
		case leftBrac == "" && rightBrac != "":
			amend = 3 // space and square brackets
		}
		if c.showDescriptionAtLineEnd {
			amend += 1 // another space
		}

		statusWhenFinishing := 1
		if s.finished {
			statusWhenFinishing = 3
		}

		c.width = width - getStringWidth(c, s.description, true) - 10 - amend - sb.Len() - len(leftBrac) - len(rightBrac) - statusWhenFinishing - 1
		s.currentSaucerSize = int(float64(s.currentPercent) / 100.0 * float64(c.width))
	}
	if s.currentSaucerSize > 0 {
		if c.ignoreLength {
			saucer = strings.Repeat(c.theme.SaucerPadding, s.currentSaucerSize-1)
		} else {
			saucer = strings.Repeat(c.theme.Saucer, s.currentSaucerSize-1)
		}

		// Check if an alternate saucer head is set for animation
		if c.theme.AltSaucerHead != "" && s.isAltSaucerHead {
			saucerHead = c.theme.AltSaucerHead
			s.isAltSaucerHead = false
		} else if c.theme.SaucerHead == "" || s.currentSaucerSize == c.width {
			// use the saucer for the saucer head if it hasn't been set
			// to preserve backwards compatibility
			saucerHead = c.theme.Saucer
		} else {
			saucerHead = c.theme.SaucerHead
			s.isAltSaucerHead = true
		}
	}

	/*
		Progress Bar format
		Description % |------        |  (kb/s) (iteration count) (iteration rate) (predict time)

		or if showDescriptionAtLineEnd is enabled
		% |------        |  (kb/s) (iteration count) (iteration rate) (predict time) Description
	*/

	repeatAmount := c.width - s.currentSaucerSize
	if repeatAmount < 0 {
		repeatAmount = 0
	}

	str := ""

	if c.ignoreLength {
		selectedSpinner := spinners[c.spinnerType]
		if len(c.spinner) > 0 {
			selectedSpinner = c.spinner
		}
		spinner := selectedSpinner[int(math.Round(math.Mod(float64(time.Since(s.startTime).Milliseconds()/100), float64(len(selectedSpinner)))))]
		if c.elapsedTime {
			if c.showDescriptionAtLineEnd {
				str = fmt.Sprintf("\r%s %s [%s] %s ",
					spinner,
					sb.String(),
					leftBrac,
					s.description)
			} else {
				str = fmt.Sprintf("\r%s %s %s [%s] ",
					spinner,
					s.description,
					sb.String(),
					leftBrac)
			}
		} else if s.finished && !s.completed {
			if c.showDescriptionAtLineEnd {
				str = fmt.Sprintf("\r%s %s | %s | %s ",
					spinner,
					sb.String(),
					"finishing",
					s.description,
				)
			} else {
				str = fmt.Sprintf("\r%s %s %s | %s",
					spinner,
					s.description,
					sb.String(),
					"finishing",
				)
			}
		} else if s.finished && s.completed {
			if c.showDescriptionAtLineEnd {
				str = fmt.Sprintf("\r%s %s | %s | %s ",
					spinner,
					sb.String(),
					"completed",
					s.description,
				)
			} else {
				str = fmt.Sprintf("\r%s %s %s | %s",
					spinner,
					s.description,
					sb.String(),
					"completed",
				)
			}
		}
	} else if rightBrac == "" {
		str = fmt.Sprintf("%5d%% %s%s%s%s%s %s",
			s.currentPercent,
			c.theme.BarStart,
			saucer,
			saucerHead,
			strings.Repeat(c.theme.SaucerPadding, repeatAmount),
			c.theme.BarEnd,
			sb.String())

		if s.currentPercent == 100 && c.showElapsedTimeOnFinish {
			str = fmt.Sprintf("%s [%s]", str, leftBrac)
		}
		if s.currentPercent == 100 && s.finished && !s.completed {
			str = fmt.Sprintf("%s | %s", str, "finishing")
		} else if s.currentPercent == 100 && s.finished && s.completed {
			str = fmt.Sprintf("%s | %s", str, "completed")
		}

		if c.showDescriptionAtLineEnd {
			str = fmt.Sprintf("\r%s %s ", str, s.description)
		} else {
			str = fmt.Sprintf("\r%s%s ", s.description, str)
		}
	} else {
		if s.currentPercent == 100 {
			str = fmt.Sprintf("%5d%% %s%s%s%s%s %s",
				s.currentPercent,
				c.theme.BarStart,
				saucer,
				saucerHead,
				strings.Repeat(c.theme.SaucerPadding, repeatAmount),
				c.theme.BarEnd,
				sb.String())

			if c.showElapsedTimeOnFinish {
				str = fmt.Sprintf("%s [%s]", str, leftBrac)
			}

			if s.finished && !s.completed {
				str = fmt.Sprintf("%s | %s", str, "finishing")
			} else if s.finished && s.completed {
				str = fmt.Sprintf("%s | %s", str, "completed")
			}

			if c.showDescriptionAtLineEnd {
				str = fmt.Sprintf("\r%s %s", str, s.description)
			} else {
				str = fmt.Sprintf("\r%s%s", s.description, str)
			}
		} else {
			str = fmt.Sprintf("%5d%% %s%s%s%s%s %s [%s:%s]",
				s.currentPercent,
				c.theme.BarStart,
				saucer,
				saucerHead,
				strings.Repeat(c.theme.SaucerPadding, repeatAmount),
				c.theme.BarEnd,
				sb.String(),
				leftBrac,
				rightBrac)

			if c.showDescriptionAtLineEnd {
				str = fmt.Sprintf("\r%s %s", str, s.description)
			} else {
				str = fmt.Sprintf("\r%s%s", s.description, str)
			}
		}
	}

	if c.colorCodes {
		// convert any color codes in the bar into the respective ANSI codes
		str = colorstring.Color(str)
	}

	s.rendered = str

	return getStringWidth(c, str, false), str, nil
}

func writeToProgress(c progressConfig, out []byte) error {
	if _, err := c.writer.Write(out); err != nil {
		return err
	}
	// if _, err := io.WriteString(c.writer, str); err != nil {
	// 	return err
	// }

	if f, ok := c.writer.(*os.File); ok {
		// ignore any errors in Sync(), as stdout
		// can't be synced on some operating systems
		// like Debian 9 (Stretch)
		f.Sync()
	}

	return nil
}

// termSize function returns the visible width and heigth of the current terminal
// and can be redefined for testing
func termSize() (w, h int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 25
	}
	return w, h
}
