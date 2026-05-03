//go:build ignore

// This is a manual test program to verify CLI invokers work with real LLM CLIs.
// Run with: go run test_invokers.go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mshykov/local-review/internal/cli"
)

func main() {
	fmt.Println("Testing LLM CLI Invokers")
	fmt.Println("========================\n")

	// Sample diff for testing
	sampleDiff := `diff --git a/main.go b/main.go
index 1234567..abcdefg 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {
+    fmt.Println("Hello")
 }`

	// Detect all LLMs
	llms := cli.DetectAll()

	for _, llm := range llms {
		if !llm.Available {
			fmt.Printf("⊘ %s: not installed (skipping)\n\n", llm.Name)
			continue
		}

		fmt.Printf("Testing %s (v%s)...\n", llm.Name, llm.Version)
		fmt.Printf("Path: %s\n", llm.Path)

		invoker := cli.NewInvoker(llm)
		if invoker == nil {
			fmt.Printf("✗ Failed to create invoker\n\n")
			continue
		}

		// Create context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		// Run review
		start := time.Now()
		output, err := invoker.Review(ctx, sampleDiff)
		duration := time.Since(start)

		if err != nil {
			fmt.Printf("✗ Review failed: %v\n", err)
			fmt.Printf("Duration: %v\n\n", duration)
			cancel() // Explicitly cancel context
			continue
		}

		fmt.Printf("✓ Review completed in %v\n", duration)
		fmt.Printf("Output length: %d bytes\n", len(output))
		fmt.Printf("Output preview:\n%s\n", truncate(output, 200))
		fmt.Println()
		cancel() // Explicitly cancel context
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}
