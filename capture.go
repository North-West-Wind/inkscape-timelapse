package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	dbusDest = "org.inkscape.Inkscape"
	dbusPath = "/org/inkscape/Inkscape"
	tlAttr   = "data-tlid"
)

var tlAttrRE = regexp.MustCompile(`\s+data-tlid="[^"]*"`)

// ── D-Bus helpers ─────────────────────────────────────────────────

func activate(conn *dbus.Conn, action string, param []dbus.Variant) error {
	obj := conn.Object(dbusDest, dbus.ObjectPath(dbusPath))
	return obj.Call("org.gtk.Actions.Activate", 0, action, param, map[string]dbus.Variant{}).Err
}

func captureSVG(conn *dbus.Conn) (string, error) {
	tmpFile, err := os.CreateTemp("", "inkscape-export-*.svg")
	if err != nil {
		return "", fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := activate(conn, "export-filename", []dbus.Variant{dbus.MakeVariant(tmpPath)}); err != nil {
		return "", fmt.Errorf("export-filename: %w", err)
	}
	if err := activate(conn, "export-plain-svg", []dbus.Variant{dbus.MakeVariant(true)}); err != nil {
		return "", fmt.Errorf("export-plain-svg: %w", err)
	}
	if err := activate(conn, "export-do", []dbus.Variant{}); err != nil {
		return "", fmt.Errorf("export-do: %w", err)
	}

	svg, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("read exported SVG: %w", err)
	}
	return string(svg), nil
}

func injectMarker(conn *dbus.Conn, tlid string) bool {
	for _, step := range []struct {
		action string
		param  []dbus.Variant
	}{
		{"selection-set-backup", nil},
		{"select-all", []dbus.Variant{dbus.MakeVariant("all")}},
		{"object-set-attribute", []dbus.Variant{dbus.MakeVariant(tlAttr + "," + tlid)}},
		{"selection-restore-backup", nil},
	} {
		if err := activate(conn, step.action, step.param); err != nil {
			fmt.Fprintf(os.Stderr, "Marker step %q failed: %v\n", step.action, err)
			return false
		}
	}
	return true
}

var _hasRemoveAttr int // 0=untested, 1=yes, -1=no

func stripMarker(conn *dbus.Conn) {
	if _hasRemoveAttr == -1 {
		return
	}

	for _, step := range []struct {
		action string
		param  []dbus.Variant
	}{
		{"selection-set-backup", nil},
		{"select-all", []dbus.Variant{dbus.MakeVariant("all")}},
		{"object-remove-attribute", []dbus.Variant{dbus.MakeVariant(tlAttr)}},
		{"selection-restore-backup", nil},
	} {
		if err := activate(conn, step.action, step.param); err != nil {
			_hasRemoveAttr = -1
			return
		}
	}
	_hasRemoveAttr = 1
}

func checkWindowCount(conn *dbus.Conn) int {
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return 0
	}
	found := false
	for _, n := range names {
		if n == dbusDest {
			found = true
			break
		}
	}
	if !found {
		return 0
	}

	var xml string
	winObj := conn.Object(dbusDest, dbus.ObjectPath(dbusPath+"/window"))
	if err := winObj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(xml, "\n") {
		if strings.Contains(line, "<node") {
			count++
		}
	}
	return count
}

// ── Start ─────────────────────────────────────────────────────────

func recordCapture(f recordFlags) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "D-Bus connection failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if n := checkWindowCount(conn); n == 0 {
		fmt.Fprintln(os.Stderr, "No Inkscape window detected on D-Bus.")
		os.Exit(1)
	}

	tlid := time.Now().Format("2006-01-02_15-04-05")

	if !injectMarker(conn, tlid) {
		fmt.Fprintln(os.Stderr, "Failed to inject recording marker.")
		os.Exit(1)
	}
	defer stripMarker(conn)

	svgRaw, err := captureSVG(conn)
	if err != nil || svgRaw == "" {
		fmt.Fprintln(os.Stderr, "Failed to capture initial SVG. Is Inkscape running with an open document?")
		os.Exit(1)
	}

	marker := fmt.Sprintf(`%s="%s"`, tlAttr, tlid)
	if !strings.Contains(svgRaw, marker) {
		fmt.Fprintln(os.Stderr, "Recording marker not found in exported SVG. Wrong window?")
		os.Exit(1)
	}

	var tw *TLDWriter
	lastSVG := ""
	var recPath string
	totalBytes := 0

	if f.appendPath != "" {
		p := f.appendPath
		var captures int
		var prevBytes int
		var err error
		tw, lastSVG, captures, prevBytes, err = OpenTLDAppend(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open recording for append: %v\n", err)
			os.Exit(1)
		}
		recPath = p
		totalBytes = prevBytes
		fmt.Printf("Appending to %s (%d existing captures)\n", filepath.Base(recPath), captures)
	} else {
		savePath := f.saveDir
		if savePath == "" {
			var err error
			savePath, err = os.Getwd()
			if err != nil {
				savePath = "."
			}
		}
		if err := os.MkdirAll(savePath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Cannot create save directory: %v\n", err)
			os.Exit(1)
		}
		recPath = filepath.Join(savePath, tlid+".tld")
		var err error
		tw, err = CreateTLD(recPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create recording: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Recording started: %s\n", filepath.Base(recPath))
	}
	defer tw.Close()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// ENTER handling
	scanCh := make(chan string, 1)
	go func() {
		defer close(scanCh)
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			scanCh <- scan.Text()
		}
	}()

	fmt.Println("Recording (ENTER to pause, Ctrl+C to stop)...")

	var lastChangeUnix int64
	curInterval := f.interval

	for {
		select {
		case <-sigCh:
			fmt.Println("\nStopping recording...")
			return
		case <-scanCh:
			fmt.Print("Paused recording. Press ENTER to resume")
			<-scanCh
		default:
		}

		tStart := time.Now()
		captureTick(conn, tw, tlid, &lastSVG, f, &lastChangeUnix, &totalBytes, &curInterval)
		elapsed := time.Since(tStart)

		if sleep := curInterval - elapsed; sleep > 0 {
			timer := time.NewTimer(sleep)
			select {
			case <-sigCh:
				timer.Stop()
				fmt.Println("\nStopping recording...")
				return
			case <-timer.C:
			}
		}
	}
}

func captureTick(conn *dbus.Conn, tw *TLDWriter, tlid string, lastSVG *string, f recordFlags, lastChangeUnix *int64, totalBytes *int, curInterval *time.Duration) {
	svgRaw, err := captureSVG(conn)
	if err != nil || svgRaw == "" {
		return
	}

	marker := fmt.Sprintf(`%s="%s"`, tlAttr, tlid)
	if !strings.Contains(svgRaw, marker) {
		// Non-recording window focused: widen interval to reduce crash risk
		if *curInterval < time.Second {
			*curInterval = time.Second
			fmt.Fprintf(os.Stderr, "\nNon-recording window focused, slowing to 1s intervals\n")
		}
		if f.skipUnchanged {
			return
		}
		ts := uint64(time.Now().UnixMilli())
		tw.WriteCapture(ts, "")
		printStatus(ts, *lastChangeUnix, *totalBytes)
		return
	}

	// Recording window focused: restore user interval
	if *curInterval != f.interval {
		*curInterval = f.interval
		fmt.Fprintf(os.Stderr, "\nRecording window refocused, restoring interval\n")
	}

	svgStr := cleanSVG(svgRaw)
	if svgStr == *lastSVG && f.skipUnchanged {
		return
	}

	diffText := ""
	if svgStr != *lastSVG {
		diffText = generateDiff(*lastSVG, svgStr)
	}

	ts := uint64(time.Now().UnixMilli())
	if err := tw.WriteCapture(ts, diffText); err != nil {
		fmt.Fprintf(os.Stderr, "\nWrite capture failed: %v\n", err)
		return
	}
	*totalBytes += len(diffText)
	if isNonEmpty(diffText) {
		*lastChangeUnix = int64(ts)
		*lastSVG = svgStr
	}
	printStatus(ts, *lastChangeUnix, *totalBytes)
}

func printStatus(ts uint64, lastChangeUnix int64, totalBytes int) {
	t := time.UnixMilli(int64(ts))
	if lastChangeUnix > 0 {
		delta := time.Since(time.UnixMilli(lastChangeUnix))
		fmt.Printf("\r[%s] last change: %+.1fs  %d bytes  ",
			t.Format("15:04:05.000"), delta.Seconds(), totalBytes)
	} else {
		fmt.Printf("\r[%s] no changes yet  %d bytes  ",
			t.Format("15:04:05.000"), totalBytes)
	}
}

// ── Helpers ───────────────────────────────────────────────────────

func cleanSVG(svgText string) string {
	return tlAttrRE.ReplaceAllString(svgText, "")
}
