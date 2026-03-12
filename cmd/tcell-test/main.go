package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/terminfo"
	"golang.org/x/term"
)

func main() {
	fmt.Println("=== TCell Environment Test ===")
	fmt.Printf("TERM=%s\n", os.Getenv("TERM"))
	fmt.Printf("TERMINFO=%s\n", os.Getenv("TERMINFO"))
	fmt.Printf("stdin is terminal: %v\n", term.IsTerminal(int(syscall.Stdin)))
	fmt.Printf("stdout is terminal: %v\n", term.IsTerminal(int(syscall.Stdout)))

	// Test terminfo lookup
	fmt.Println("\nTesting terminfo lookup...")
	lookupDone := make(chan struct {
		ti  tcell.Terminfo
		err error
	})
	go func() {
		ti, err := tcell.LookupTerminfo(os.Getenv("TERM"))
		lookupDone <- struct {
			ti  tcell.Terminfo
			err error
		}{ti, err}
	}()
	select {
	case result := <-lookupDone:
		if result.err != nil {
			fmt.Printf("LookupTerminfo error: %v\n", result.err)
		} else if result.ti == nil {
			fmt.Println("LookupTerminfo: returned nil")
		} else {
			fmt.Printf("LookupTerminfo: found %s\n", result.ti.Name)
		}
	case <-time.After(5 * time.Second):
		fmt.Println("LookupTerminfo: TIMEOUT after 5 seconds!")
	}

	// Check /dev/tty
	if _, err := os.Stat("/dev/tty"); err != nil {
		fmt.Printf("/dev/tty: %v\n", err)
	} else {
		f, err := os.Open("/dev/tty")
		if err != nil {
			fmt.Printf("/dev/tty open: %v\n", err)
		} else {
			fmt.Println("/dev/tty: accessible")
			f.Close()
		}
	}

	// Test tcell.NewScreen with timeout
	fmt.Println("\nTesting tcell.NewScreen()...")
	done := make(chan struct {
		screen tcell.Screen
		err    error
	})

	go func() {
		s, err := tcell.NewScreen()
		done <- struct {
			screen tcell.Screen
			err    error
		}{s, err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			fmt.Printf("NewScreen error: %v\n", result.err)
			os.Exit(1)
		}
		fmt.Println("NewScreen: success")

		// Try to initialize
		fmt.Println("Testing screen.Init()...")
		initDone := make(chan error)
		go func() {
			initDone <- result.screen.Init()
		}()

		select {
		case err := <-initDone:
			if err != nil {
				fmt.Printf("Init error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Init: success!")
			result.screen.Fini()

		case <-time.After(5 * time.Second):
			fmt.Println("Init: TIMEOUT after 5 seconds!")
			os.Exit(1)
		}

	case <-time.After(5 * time.Second):
		fmt.Println("NewScreen: TIMEOUT after 5 seconds!")
		os.Exit(1)
	}

	fmt.Println("\nAll tests passed!")
}
