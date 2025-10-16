package doctest

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// RunnableExample represents a testable code example extracted from markdown
type RunnableExample struct {
	Name    string // Unique name from the marker
	Source  string // Source file path
	Code    string // Terraform code to test
	LineNum int    // Starting line number in source file
}

var (
	// Matches: <!-- runnable-test: example-name -->
	startMarkerRegex = regexp.MustCompile(`<!--\s*runnable-test:\s*(\S+)\s*-->`)
	// Matches: <!-- /runnable-test -->
	endMarkerRegex = regexp.MustCompile(`<!--\s*/runnable-test\s*-->`)
	// Matches: ```terraform or ```hcl
	codeBlockRegex = regexp.MustCompile("^```(terraform|hcl)")
	codeEndRegex   = regexp.MustCompile("^```$")
)

// ExtractRunnableExamples parses a markdown file and extracts all runnable examples
func ExtractRunnableExamples(filepath string) ([]RunnableExample, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", filepath, err)
	}
	defer file.Close()

	var examples []RunnableExample
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Look for start marker
		if match := startMarkerRegex.FindStringSubmatch(line); match != nil {
			exampleName := match[1]
			example := RunnableExample{
				Name:    exampleName,
				Source:  filepath,
				LineNum: lineNum,
			}

			// Extract code blocks until end marker
			code, endLine, err := extractCodeUntilEndMarker(scanner, &lineNum)
			if err != nil {
				return nil, fmt.Errorf("failed to extract example %s at line %d: %w", exampleName, lineNum, err)
			}

			example.Code = code
			examples = append(examples, example)

			lineNum = endLine
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading %s: %w", filepath, err)
	}

	return examples, nil
}

// extractCodeUntilEndMarker reads lines until it finds the end marker,
// extracting code from any terraform/hcl code blocks along the way
func extractCodeUntilEndMarker(scanner *bufio.Scanner, lineNum *int) (string, int, error) {
	var codeBlocks []string
	inCodeBlock := false
	var currentBlock strings.Builder

	for scanner.Scan() {
		*lineNum++
		line := scanner.Text()

		// Check for end marker
		if endMarkerRegex.MatchString(line) {
			// If we were in a code block, close it
			if inCodeBlock {
				codeBlocks = append(codeBlocks, currentBlock.String())
			}
			return strings.Join(codeBlocks, "\n\n"), *lineNum, nil
		}

		// Check for code block start
		if !inCodeBlock && codeBlockRegex.MatchString(line) {
			inCodeBlock = true
			currentBlock.Reset()
			continue
		}

		// Check for code block end
		if inCodeBlock && codeEndRegex.MatchString(line) {
			inCodeBlock = false
			codeBlocks = append(codeBlocks, currentBlock.String())
			continue
		}

		// If we're in a code block, collect the line
		if inCodeBlock {
			if currentBlock.Len() > 0 {
				currentBlock.WriteString("\n")
			}
			currentBlock.WriteString(line)
		}
	}

	return "", *lineNum, fmt.Errorf("reached end of file without finding closing runnable-test marker")
}

// ExtractFromMultipleFiles extracts runnable examples from multiple markdown files
func ExtractFromMultipleFiles(filepaths []string) ([]RunnableExample, error) {
	var allExamples []RunnableExample

	for _, filepath := range filepaths {
		examples, err := ExtractRunnableExamples(filepath)
		if err != nil {
			return nil, err
		}
		allExamples = append(allExamples, examples...)
	}

	return allExamples, nil
}
