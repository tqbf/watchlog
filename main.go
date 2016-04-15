package main

// Even with watchify and incremental builds, Javascript projects take so fucking
// long to build that I can't sit and wait for them to run. I had this routine where,
// every time I saved a JSX file, I'd switch to the term window with watchify running
// and whack enter a couple times so I'd be able to tell when the compile completed
// (otherwise, the lines all run together) --- this is super important because otherwise
// there's a risk that you reload your browser with old code and then you're debugging
// a stale build and jesus what a nightmare all this stuff is.
//
// Anyways, instead of doing that, this thingy runs commands, collects their output,
// colorizes based on recency, and prints an updating, human-sensible timestamp for
// each line. As a bonus, it'll regex out things from the log.
//
// I basically threw code at this until it did roughly what I wanted to and then stopped,
// so don't expect it to do anything.
//
// I was going to handle stderr/stdout differently, but, fuck it.
//
// $ watchlog -gsub '\[.*\]' gulp watchify

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/nsf/termbox-go"
)

const (
	SERR = iota
	SOUT
)

type Row struct {
	At   time.Time
	Src  int
	Line string
}

type Console struct {
	RegErr []*regexp.Regexp
	RepErr []string
	RegOut []*regexp.Regexp
	RepOut []string
	InErr  chan []byte
	InOut  chan []byte
	Lines  []Row
}

func NewConsole() *Console {
	return &Console{
		InErr: make(chan []byte),
		InOut: make(chan []byte),
	}
}

func (c *Console) inline(buf []byte, src int) {
	rxs := c.RegOut
	rps := c.RepOut
	if src == SERR {
		rxs = c.RegErr
		rps = c.RepOut
	}

	line := string(buf)

	for rxi, rx := range rxs {
		line = rx.ReplaceAllString(line, rps[rxi])
	}

	c.Lines = append(c.Lines, Row{
		At:   time.Now(),
		Src:  src,
		Line: line,
	})
}

var Hotness = []termbox.Attribute{
	termbox.ColorWhite | termbox.AttrBold,
	termbox.ColorCyan | termbox.AttrBold,
	termbox.ColorGreen | termbox.AttrBold,
	termbox.ColorBlue | termbox.AttrBold,
	termbox.ColorCyan,
	termbox.ColorBlue,
}

func (c *Console) redraw() {
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	w, h := termbox.Size()

	lines := c.Lines
	if h < len(lines) {
		off := len(lines) - h
		lines = lines[off:]
	}

	y := 0
	x := 0

	if h > len(lines) {
		y += h - len(lines)
	}

	for _, line := range lines {
		x = 0

		fg := Hotness[5]

		d := time.Since(line.At)
		switch {
		case d.Seconds() < 5:
			fg = Hotness[0]
		case d.Seconds() < 30:
			fg = Hotness[1]
		case d.Seconds() < 60:
			fg = Hotness[2]
		case d.Seconds() < 120:
			fg = Hotness[3]
		case d.Seconds() < (5 * 60):
			fg = Hotness[4]
		}

		var ts string

		switch {
		case d.Minutes() > 60:
			ts = fmt.Sprintf("[+%0.2fh] ", d.Hours())
		case d.Seconds() > 60:
			ts = fmt.Sprintf("[+%0.2fm] ", d.Minutes())
		default:
			ts = fmt.Sprintf("[+%0.2fs] ", d.Seconds())
		}

		xoff := len(ts)
		for i, chr := range ts {
			if i < w {
				termbox.SetCell(x+i, y, chr, termbox.ColorGreen, termbox.ColorDefault)
			}
		}

		for i, chr := range line.Line {
			i += xoff
			if i < w {
				termbox.SetCell(x+i, y, chr, fg, termbox.ColorDefault)
			}
		}

		y += 1
	}

	termbox.Flush()
}

func (c *Console) AddRx(rx string, rep string, src int) {
	regex, err := regexp.Compile(rx)
	if err != nil {
		log.Fatalf("can't parse <<%s>>: %s", rx, err)
	}
	if src == SERR {
		c.RegErr = append(c.RegErr, regex)
		c.RepErr = append(c.RepErr, rep)
	} else {
		c.RegOut = append(c.RegOut, regex)
		c.RepOut = append(c.RepOut, rep)
	}
}

func (c *Console) Loop() {
	t := time.NewTicker(5 * time.Second)

	c.redraw()

	for {
		select {
		case buf := <-c.InErr:
			c.inline(buf, SERR)
			c.redraw()
		case buf := <-c.InOut:
			c.inline(buf, SOUT)
			c.redraw()
		case <-t.C:
			c.redraw()
		}
	}
}

func main() {
	defer func() {
		if e := recover(); e != nil {
			// XXX do something
		}
		termbox.Close()
	}()

	var rxs = flag.String("gsub", "", "rx:replacement,\\[foo\\]:[newstring],\\[bar\\]")
	flag.Parse()

	if flag.Arg(0) == "" {
		log.Fatalf("watchlog command arg...")
	}

	cmdtup := flag.Args()

	var (
		err    error
		stderr io.ReadCloser
		stdout io.ReadCloser
	)

	c := NewConsole()
	if rxs != nil && *rxs != "" {
		for _, v := range strings.Split(*rxs, ",") {
			var (
				sep int = -1
				rx  string
				rep string
			)

			for i, chr := range v {
				if chr == ':' {
					sep = i
				}
			}

			if sep != -1 {
				rx = v[0:sep]
				rep = v[sep+1:]
			} else {
				rx = v
			}

			c.AddRx(rx, rep, SOUT)
			c.AddRx(rx, rep, SERR)
		}
	}

	var cmd *exec.Cmd
	if len(cmdtup) == 1 {
		cmd = exec.Command(cmdtup[0])
	} else {
		cmd = exec.Command(cmdtup[0], cmdtup[1:]...)
	}

	stderr, err = cmd.StderrPipe()
	if err == nil {
		stdout, err = cmd.StdoutPipe()
	}
	if err != nil {
		log.Fatalf("can't get pipe: %s", err)
	}

	if err = cmd.Start(); err != nil {
		log.Fatalf("can't run: %s", err)
	}

	termbox.Init()

	done := make(chan bool)
	go c.Loop()

	go func() {
		for {
			ev := termbox.PollEvent()
			if ev.Ch == 'q' {
				close(done)
			}
		}
	}()

	go func() {
		reader := bufio.NewReader(stderr)

		for {
			line, _ := reader.ReadBytes('\n')
			c.InErr <- line
		}
	}()

	go func() {
		reader := bufio.NewReader(stdout)

		for {
			line, _ := reader.ReadBytes('\n')
			c.InOut <- line
		}
	}()

	go func() {
		cmd.Wait()
		close(done)
	}()

	<-done

	os.Exit(1)
}
