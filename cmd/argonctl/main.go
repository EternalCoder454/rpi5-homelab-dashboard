// Command argonctl is a small Go replacement for the persistent Argon ONE
// "argononed" daemon. It drives the case fan from the temperature curve in
// /etc/argononed.conf (re-read live, so dashboard / argonone-config changes
// apply within seconds with no restart) and handles the power button.
//
// It deliberately does NOT reimplement power-off signaling: the stock one-shot
// hook /lib/systemd/system-shutdown/argon-shutdown.sh still runs at shutdown
// and signals the case MCU. This daemon only replaces the always-running
// SERVICE process (fan + button threads).
//
// Fan I2C protocol (addr 0x1a, register mode): write {0x80, duty} to set,
// write {0x80} then read 1 byte to read. Button: gpiochip4 line 4, both edges;
// the MCU encodes the request in the high-pulse width.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/warthog618/go-gpiocdev"
	"golang.org/x/sys/unix"
)

// calLogPath is on the NVMe (persistent) so calibration survives a power-off.
const calLogPath = "/var/tmp/argonctl-cal.log"

const (
	i2cPath   = "/dev/i2c-1"
	i2cSlave  = 0x0703 // I2C_SLAVE ioctl
	argonAddr = 0x1a
	regDuty   = 0x80
	confPath  = "/etc/argononed.conf"
	tempPath  = "/sys/class/thermal/thermal_zone0/temp"
	gpioChip  = "gpiochip0" // pinctrl-rp1, the 40-pin header (gpiochip4 is a symlink to it)
	btnOffset = 4
	writeWait = 1 * time.Second // the Argon MCU needs ~1s between register writes (matches argonregister.py)
	fanPoll   = 3 * time.Second
)

var (
	verbose   bool
	calibrate bool
)

func vlog(f string, a ...any) {
	if verbose {
		log.Printf(f, a...)
	}
}

// ---- I2C fan controller -------------------------------------------------

type fanCtl struct {
	fd  int
	mu  sync.Mutex
	reg bool
}

func openFan() (*fanCtl, error) {
	fd, err := unix.Open(i2cPath, unix.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.IoctlSetInt(fd, i2cSlave, argonAddr); err != nil {
		unix.Close(fd)
		return nil, err
	}
	f := &fanCtl{fd: fd}
	f.reg = f.checkSupport()
	return f, nil
}

func (f *fanCtl) writeReg(reg, val byte) error {
	_, err := unix.Write(f.fd, []byte{reg, val})
	time.Sleep(writeWait)
	return err
}

func (f *fanCtl) readReg(reg byte) (byte, error) {
	if _, err := unix.Write(f.fd, []byte{reg}); err != nil {
		return 0, err
	}
	time.Sleep(10 * time.Millisecond) // let the MCU latch the register pointer before read
	buf := make([]byte, 1)
	if _, err := unix.Read(f.fd, buf); err != nil {
		return 0, err
	}
	return buf[0], nil
}

// checkSupport detects whether the controller exposes the duty-cycle register
// (write a changed value, read it back, restore) — mirrors argonregister.
func (f *fanCtl) checkSupport() bool {
	old, err := f.readReg(regDuty)
	if err != nil {
		return false
	}
	nv := old + 1
	if nv >= 100 {
		nv = 98
	}
	if f.writeReg(regDuty, nv) != nil {
		return false
	}
	got, _ := f.readReg(regDuty)
	if got != old {
		f.writeReg(regDuty, old)
		return true
	}
	return false
}

func (f *fanCtl) set(speed int) {
	if speed < 0 {
		speed = 0
	}
	if speed > 100 {
		speed = 100
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reg {
		f.writeReg(regDuty, byte(speed))
	} else {
		unix.Write(f.fd, []byte{byte(speed)})
		time.Sleep(writeWait)
	}
}

// ---- fan curve ----------------------------------------------------------

type point struct {
	temp  float64
	speed int
}

func defaultCurve() []point { return []point{{65, 100}, {60, 55}, {55, 30}} }

func loadCurve() []point {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return defaultCurve()
	}
	var c []point
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		t, e1 := strconv.ParseFloat(strings.TrimSpace(kv[0]), 64)
		s, e2 := strconv.Atoi(strings.TrimSpace(kv[1]))
		if e1 == nil && e2 == nil {
			c = append(c, point{t, s})
		}
	}
	if len(c) == 0 {
		return defaultCurve()
	}
	sort.Slice(c, func(i, j int) bool { return c[i].temp > c[j].temp })
	return c
}

func cpuTemp() float64 {
	b, err := os.ReadFile(tempPath)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0
	}
	return v / 1000
}

func targetFor(temp float64, c []point) int {
	for _, p := range c { // sorted high->low: first threshold <= temp wins
		if temp >= p.temp {
			return p.speed
		}
	}
	return 0
}

func fanLoop(f *fanCtl) {
	current := -1
	for {
		t := cpuTemp()
		target := targetFor(t, loadCurve())
		if target != current {
			vlog("fan: %d%% -> %d%% (cpu %.1fC)", current, target, t)
			if target > 0 {
				f.set(100) // brief spin-up to start reliably, as argononed does
			}
			f.set(target)
			current = target
		}
		time.Sleep(fanPoll)
	}
}

// ---- power button -------------------------------------------------------

// The Argon MCU emits a high pulse on BCM4 whose width encodes the request.
// argononed measured it by polling every 10ms: count 2-3 -> reboot,
// 4-5 -> shutdown, 6-7 -> OLED. We measure the real width from edge
// timestamps; thresholds below are calibrated against actual presses.
var riseAt time.Duration

func handleButton(evt gpiocdev.LineEvent) {
	if evt.Type == gpiocdev.LineEventRisingEdge {
		riseAt = evt.Timestamp
		return
	}
	if riseAt == 0 {
		return
	}
	ms := (evt.Timestamp - riseAt).Milliseconds()
	riseAt = 0

	action := ""
	switch {
	case ms >= 15 && ms <= 37:
		action = "reboot"
	case ms >= 38 && ms <= 60:
		action = "poweroff"
	}

	if calibrate {
		a := action
		if a == "" {
			a = "(none)"
		}
		log.Printf("button(cal): pulse=%dms action=%s", ms, a)
		if fh, err := os.OpenFile(calLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(fh, "%s pulse=%dms action=%s\n", time.Now().Format("15:04:05"), ms, a)
			fh.Close()
		}
		return
	}

	if action == "" {
		vlog("button: pulse %dms (no action)", ms)
		return
	}
	log.Printf("button: pulse %dms -> %s", ms, action)
	_ = exec.Command("systemctl", action).Start()
}

func buttonLoop() {
	l, err := gpiocdev.RequestLine(gpioChip, btnOffset,
		gpiocdev.AsInput, gpiocdev.WithBothEdges, gpiocdev.WithEventHandler(handleButton))
	if err != nil {
		log.Printf("button: gpio request failed: %v (button disabled)", err)
		return
	}
	defer l.Close()
	log.Printf("button: watching %s line %d", gpioChip, btnOffset)
	select {} // events arrive via the handler; block forever
}

func main() {
	flag.BoolVar(&verbose, "v", false, "verbose logging")
	flag.BoolVar(&calibrate, "cal", false, "log button pulses but do NOT reboot/poweroff")
	flag.Parse()
	log.SetFlags(log.Ltime)

	f, err := openFan()
	if err != nil {
		log.Fatalf("i2c open failed: %v", err)
	}
	log.Printf("argonctl started (fan register-mode=%v, calibrate=%v)", f.reg, calibrate)

	go buttonLoop()
	fanLoop(f)
}
