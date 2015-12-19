package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type command struct {
	line        string
	endLine     string
	lineNo      int
	fields      []string
	subCommands []command
}

type program []command

func parseNumber(f string, lineNo int) int {
	duration, err := strconv.Atoi(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error in line %d\n", lineNo)
		os.Exit(1)
	}
	return duration
}

func parseCount(f string, lineNo int) int {
	duration := parseNumber(f, lineNo)
	if duration == 0 {
		fmt.Fprintf(os.Stderr, "Error in line %d: count can't be zero\n", lineNo)
		os.Exit(1)
	}
	return duration
}

func splitLine(lineVerbatim string) []string {
	line := lineVerbatim
	if strings.Contains(line, ";") {
		line = line[0:strings.Index(line, ";")]
	}
	line = strings.Trim(line, " \t")
	fields := strings.Split(line, ",")
	for i, f := range fields {
		fields[i] = strings.Trim(f, " \t")
	}
	return fields
}

func parseLines(lines []string, startLineNo int) (commands []command, lineNo int) {
	lineNo = startLineNo
	for lineNo < len(lines) {
		lineVerbatim := lines[lineNo]
		fields := splitLine(lineVerbatim)
		if fields[0] == "E" {
			break
		}
		command, newLineNo := parseCommand(lines, lineNo, fields)
		commands = append(commands, command)
		lineNo = newLineNo
	}
	return commands, lineNo
}

func isBlockCommand(c string) bool {
	return c == "L" || c == "CLUBS"
}

func (c *command) hasSubCommands() bool {
	return isBlockCommand(c.fields[0])
}

func parseCommand(lines []string, startLineNo int, fields []string) (c command, lineNo int) {
	lineNo = startLineNo
	lineVerbatim := lines[lineNo]
	c = command{line: lineVerbatim, lineNo: lineNo, fields: fields}
	if fields[0] == "E" {
		panic("cannot parse command E")
	}
	if isBlockCommand(fields[0]) {
		subCommands, newLineNo := parseLines(lines, lineNo+1)
		if newLineNo >= len(lines) {
			fmt.Fprintf(os.Stderr, "Error in program: unterminated loop\n")
			os.Exit(1)
		}
		c.subCommands = subCommands
		c.endLine = lines[newLineNo]
		lineNo = newLineNo
	}
	lineNo++

	return c, lineNo
}

func (c *command) duration() int {
	switch c.fields[0] {
	case "D":
		return parseCount(c.fields[1], c.lineNo)
	case "RAMP":
		return parseCount(c.fields[4], c.lineNo)
	case "L":
		count := parseCount(c.fields[1], c.lineNo)
		duration := 0
		for _, sc := range c.subCommands {
			duration += sc.duration()
		}
		return duration * count
	case "TIME":
		fmt.Fprintf(os.Stderr, "Error: TIME not supported here in line %d\n", c.lineNo)
		os.Exit(1)
		return -1
	default:
		if c.hasSubCommands() {
			panic(fmt.Sprintf("unexpected sub-commands in %s in line %d", c.fields[0], c.lineNo))
		}
		return 0
	}
}

func (c *command) print() {
	fmt.Println(c.line)
	if c.hasSubCommands() {
		for _, sc := range c.subCommands {
			sc.print()
		}
		fmt.Println(c.endLine)
	}
}

func (p program) print() {
	for _, c := range p {
		c.print()
	}
}

func (p program) annotateTimes() {
	time := 0
	for _, c := range p {
		c.print()
		d := c.duration()
		if d > 0 {
			time += c.duration()
			fmt.Printf("    ; time %d\n", time)
		}
	}
}

func (p program) specializeForClub(club int) program {
	var newCommands []command
	for _, c := range p {
		switch c.fields[0] {
		case "CLUBS":
			found := false
			for _, f := range c.fields[1:len(c.fields)] {
				if parseCount(f, c.lineNo) == club {
					found = true
					break
				}
			}
			if found {
				subCommands := program(c.subCommands).specializeForClub(club)
				for _, sc := range subCommands {
					newCommands = append(newCommands, sc)
				}
			}
		default:
			newC := c
			if c.hasSubCommands() {
				newC.subCommands = program(c.subCommands).specializeForClub(club)
			}
			newCommands = append(newCommands, newC)
		}
	}
	return newCommands
}

func (p program) resolveTime() program {
	var newCommands []command
	time := 0
	for _, c := range p {
		switch c.fields[0] {
		case "TIME":
			target := parseCount(c.fields[1], c.lineNo)
			if target < time {
				fmt.Fprintf(os.Stderr, "Error: Cannot go back in time - it's already %d - in line %d\n", time, c.lineNo)
				os.Exit(1)
			}
			if target == time {
				continue
			}
			fields := []string{"D", fmt.Sprintf("%d", target-time)}
			newCommands = append(newCommands, command{line: strings.Join(fields, ","), fields: fields, lineNo: c.lineNo})
			time = target
		default:
			newCommands = append(newCommands, c)
			time += c.duration()
		}
	}
	return newCommands
}

type color struct {
	r, g, b int
}

var colorRegexp *regexp.Regexp

func resolveColor(colors map[string]color, description string, lineNo int) []string {
	if colorRegexp == nil {
		colorRegexp = regexp.MustCompile("^([^%]+)(\\s+(\\d+)%)?$")
	}
	matches := colorRegexp.FindStringSubmatch(description)
	name := matches[1]
	c, ok := colors[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: Color %s not defined in line %d\n", name, lineNo)
		os.Exit(1)
	}
	if matches[3] != "" {
		p := float64(parseNumber(matches[3], lineNo)) / 100.0
		c.r = int(float64(c.r) * p)
		c.g = int(float64(c.g) * p)
		c.b = int(float64(c.b) * p)
	}
	return []string{fmt.Sprintf("%d", c.r), fmt.Sprintf("%d", c.g), fmt.Sprintf("%d", c.b)}
}

func resolveColorInCommands(cs []command, colors map[string]color, allowDefine bool) []command {
	var newCommands []command
	for _, c := range cs {
		switch c.fields[0] {
		case "COLOR":
			if !allowDefine {
				fmt.Fprintf(os.Stderr, "Error: Can't define colors here in line %d\n", c.lineNo)
				os.Exit(1)
			}
			_, ok := colors[c.fields[1]]
			if ok {
				fmt.Fprintf(os.Stderr, "Error: Color %s redefined\n", c.fields[1])
				os.Exit(1)
			}
			var colorFields []string
			if len(c.fields) == 3 {
				colorFields = resolveColor(colors, c.fields[2], c.lineNo)
			} else {
				colorFields = c.fields[2:5]
			}
			colors[c.fields[1]] = color{
				r: parseNumber(colorFields[0], c.lineNo),
				g: parseNumber(colorFields[1], c.lineNo),
				b: parseNumber(colorFields[2], c.lineNo),
			}
		case "C":
			newC := c
			if len(c.fields) == 2 {
				clr := resolveColor(colors, c.fields[1], c.lineNo)
				newC.fields = []string{"C", clr[0], clr[1], clr[2]}
				newC.line = strings.Join(newC.fields, ",")
			}
			newCommands = append(newCommands, newC)
		case "RAMP":
			newC := c
			if len(c.fields) == 3 {
				clr := resolveColor(colors, c.fields[1], c.lineNo)
				newC.fields = []string{"RAMP", clr[0], clr[1], clr[2], c.fields[2]}
				newC.line = strings.Join(newC.fields, ",")
			}
			newCommands = append(newCommands, newC)
		default:
			newC := c
			if c.hasSubCommands() {
				newC.subCommands = resolveColorInCommands(c.subCommands, colors, false)
			}
			newCommands = append(newCommands, newC)
		}
	}
	return newCommands
}

func (p program) resolveColor() program {
	return resolveColorInCommands(p, make(map[string]color), true)
}

type label struct {
	start int
	end   int
}

func (p program) resolveLabels(labels map[string]label) program {
	var newCommands []command
	for _, c := range p {
		switch c.fields[0] {
		case "TIME":
			newC := c
			allDigits, err := regexp.MatchString("^\\d+$", c.fields[1])
			if err != nil {
				panic("We screwed up the regular expression")
			}
			if !allDigits {
				label, ok := labels[c.fields[1]]
				if !ok {
					fmt.Fprintf(os.Stderr, "Error: Label %s not defined\n", c.fields[1])
					os.Exit(1)
				}
				newC.fields = []string{"TIME", fmt.Sprintf("%d", label.start)}
				newC.line = strings.Join(newC.fields, ",")
			}
			newCommands = append(newCommands, newC)
		default:
			newC := c
			if c.hasSubCommands() {
				newC.subCommands = program(c.subCommands).resolveLabels(labels)
			}
			newCommands = append(newCommands, newC)
		}
	}
	return newCommands
}

func parseProgram(r io.Reader) program {
	scanner := bufio.NewScanner(r)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	commands, lineNo := parseLines(lines, 0)
	if lineNo < len(lines) {
		fmt.Fprintf(os.Stderr, "Error in line %d: E without L\n", lineNo)
		os.Exit(1)
	}

	return commands
}

// XMLLabel must be exported to work with encoding/xml.
type XMLLabel struct {
	Title string  `xml:"title,attr"`
	Start float64 `xml:"t,attr"`
	End   float64 `xml:"t1,attr"`
}

// XMLProject must be exported to work with encoding/xml.
type XMLProject struct {
	Labels []XMLLabel `xml:"labeltrack>label"`
}

func readLabels(reader io.Reader) (map[string]label, error) {
	var project XMLProject
	if err := xml.NewDecoder(reader).Decode(&project); err != nil {
		return nil, err
	}
	labels := make(map[string]label)
	for _, l := range project.Labels {
		_, ok := labels[l.Title]
		if ok {
			fmt.Fprintf(os.Stderr, "Error: Label %s defined more than once\n", l.Title)
			os.Exit(1)
		}
		labels[l.Title] = label{start: int(l.Start * 100), end: int(l.End * 100)}
	}
	return labels, nil
}

func main() {
	file, err := os.Open("einszweipolizei.aup")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
	defer file.Close()

	labels, err := readLabels(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading XML: %s\n", err.Error())
	}

	program := parseProgram(os.Stdin)
	specialized := program.specializeForClub(1)
	colored := specialized.resolveColor()
	delabeled := colored.resolveLabels(labels)
	resolved := delabeled.resolveTime()
	resolved.annotateTimes()
}
