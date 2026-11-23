package main

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type size int64

type FileEntry struct {
	Path string
	Size size
}

type Semaphore struct{}

const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorBoldRed = "\033[1;31m"
	colorYellow  = "\033[33m"
	colorGreen   = "\033[32m"
)

func (s size) Color() string {
	switch {
	case s >= 50<<30:
		return colorBoldRed
	case s >= 1<<30:
		return colorRed
	case s >= 1<<20:
		return colorYellow
	default:
		return colorGreen
	}
}

func (s size) String() string {
	units := []string{
		" B",
		"KB",
		"MB",
		"GB",
		"TB",
		"PB",
	}

	roundedSize := float64(s)
	i := 0
	for ; roundedSize >= 1024.0 && i < len(units)-1; i++ {
		roundedSize /= 1024.0
	}
	return fmt.Sprintf("%.2f %s", roundedSize, units[i])
}

func walkConcurrent(dir string, sem chan Semaphore, wg *sync.WaitGroup, results chan<- FileEntry, v bool) {

	sem <- Semaphore{}
	entries, err := os.ReadDir(dir)
	<-sem

	if err != nil {
		if v {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", dir, err)
		}
		return
	}

	for _, d := range entries {
		fullPath := filepath.Join(dir, d.Name())
		if d.IsDir() {
			wg.Go(func() {
				walkConcurrent(fullPath, sem, wg, results, v)
			})
			continue
		}

		info, err := d.Info()
		if err != nil {
			continue
		}
		results <- FileEntry{
			Path: fullPath,
			Size: size(info.Size()),
		}
	}

}

func main() {
	var root string
	flag.StringVar(&root, "root-dir", ".", "Root directory to walk from.")

	var fileMode bool
	flag.BoolVar(&fileMode, "file", false, "If on list out files instead of directories.")

	var topSize int
	flag.IntVar(&topSize, "top", 10, "Set the amount of entries to print out.")

	var outputFile string
	flag.StringVar(&outputFile, "out", "", "Set output file.")

	var verboseMode bool
	flag.BoolVar(&verboseMode, "v", false, "Output permission errors")

	flag.Parse()

	if topSize < 0 {
		topSize = 0
	}

	var files []FileEntry

	dirSize := make(map[string]size)

	root = filepath.Clean(root)

	results := make(chan FileEntry, 256)
	sem := make(chan Semaphore, 20)
	var wg sync.WaitGroup

	wg.Go(func() {
		walkConcurrent(root, sem, &wg, results, verboseMode)
	})

	go func() {
		wg.Wait()
		close(results)
	}()

	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	var fileCount int
	var totalBytes size
	frame := 0

scanLoop:
	for {
		select {
		case entry, ok := <-results:
			if !ok {
				break scanLoop
			}
			fileCount++
			totalBytes += entry.Size
			if fileMode {
				files = append(files, entry)
				continue
			}

			parent := filepath.Dir(entry.Path)
			for {
				dirSize[parent] += entry.Size
				if parent == root {
					break
				}
				next := filepath.Dir(parent)
				if next == parent {
					break
				}
				parent = next
			}
		case <-ticker.C:
			fmt.Fprintf(os.Stderr, "\r\033[K\033[36m%s\033[0m Scanning... \033[32m%d\033[0m files (%s)",
				spinner[frame%len(spinner)], fileCount, totalBytes)
			frame++
		}
	}

	fmt.Fprintf(os.Stderr, "\r\033[K")

	if !fileMode {
		for path, total := range dirSize {
			files = append(files, FileEntry{
				Path: path,
				Size: total,
			})
		}
	}

	slices.SortFunc(files, func(a, b FileEntry) int {
		return cmp.Compare(b.Size, a.Size)
	})

	var out *os.File
	if outputFile != "" {
		var err error
		out, err = os.Create(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create output file: %v\n", err)
			os.Exit(1)
		}
		defer out.Close()
	}

	limit := min(topSize, len(files))
	for _, f := range files[:limit] {
		fmt.Printf("%s%10s%s %s\n", f.Size.Color(), f.Size, colorReset, f.Path)
		if out != nil {
			fmt.Fprintf(out, "%10s %s\n", f.Size, f.Path)
		}
	}
}
