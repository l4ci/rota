package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Every accepted composer requires an authoritative styled first paint.
// Plain text cannot distinguish continuation rows or ghost text safely.
// No bytes are ever written to a terminal stream channel.
func (h *coreHost) composer(ctx context.Context, v coreHandle, t coreTerminal, plain string) error {
	if _, ok := ReadDialog(plain); ok {
		return coreComposer(plain)
	}
	if coreComposerStart(plain) < 0 {
		return coreComposer(plain)
	}
	c, e := h.connectMode(ctx, v.Endpoint, v.Core, true)
	if e != nil {
		return fmt.Errorf("%w: styled Core observer unavailable: %v", ErrDraftOnPrompt, e)
	}
	defer c.Close()
	// json.Decoder may retain the newline following the plain hello reply.
	reader := bufio.NewReader(io.MultiReader(c.dec.Buffered(), c.Conn))
	if b, _ := reader.Peek(1); len(b) == 1 && b[0] == '\n' {
		reader.ReadByte()
	}
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "terminal.attach", "params": map[string]any{"terminal": v.Address, "cols": t.Cols, "rows": t.Rows, "observe": true, "history": 0, "resize_notifications": false}})
	if t.Cols < 1 || t.Rows < 1 || t.Cols > 500 || t.Rows > 300 {
		return fmt.Errorf("%w: unsupported Core observer grid", ErrDraftOnPrompt)
	}
	if e = coreWriteFrame(c.Conn, 0, 0, request); e != nil {
		return e
	}
	var attach struct {
		Channel  uint32
		Terminal coreTerminal
		Resize   bool `json:"resize_notifications"`
	}
	var paint []byte
	for frames := 0; frames < 128; frames++ {
		ch, kind, data, err := coreReadFrame(reader)
		if err != nil {
			return fmt.Errorf("%w: incomplete Core first paint: %v", ErrDraftOnPrompt, err)
		}
		if ch == 0 && kind == 0 {
			var r struct {
				ID     int
				Result json.RawMessage
				Error  *coreRPCError
			}
			if json.Unmarshal(data, &r) != nil || r.ID != 2 || r.Error != nil {
				return fmt.Errorf("%w: invalid Core observer reply", ErrDraftOnPrompt)
			}
			if json.Unmarshal(r.Result, &attach) != nil || attach.Channel == 0 || attach.Resize || coreProgram(v, attach.Terminal) != nil {
				return fmt.Errorf("%w: Core observer identity changed", ErrDraftOnPrompt)
			}
			continue
		}
		if attach.Channel == 0 || ch != attach.Channel || kind != 0 {
			return fmt.Errorf("%w: unexpected Core observer frame", ErrDraftOnPrompt)
		}
		paint = append(paint, data...)
		if len(paint) > 256*1024 {
			return fmt.Errorf("%w: Core first paint exceeds observer budget", ErrDraftOnPrompt)
		}
		end := -1
		for _, suffix := range []string{"\x1b[?9001h", "\x1b[?9001l"} {
			if i := bytes.Index(paint, []byte(suffix)); i >= 0 {
				end = i + len(suffix)
			}
		}
		if end >= 0 {
			return coreStyledComposer(paint[:end], attach.Terminal.Cols, attach.Terminal.Rows, plain)
		}
	}
	return fmt.Errorf("%w: Core first paint exceeded frame budget", ErrDraftOnPrompt)
}
func coreWriteFrame(w io.Writer, channel uint32, kind byte, data []byte) error {
	var header [9]byte
	binary.LittleEndian.PutUint32(header[:], channel)
	header[4] = kind
	binary.LittleEndian.PutUint32(header[5:], uint32(len(data)))
	if _, e := w.Write(header[:]); e != nil {
		return e
	}
	_, e := w.Write(data)
	return e
}
func coreReadFrame(r io.Reader) (uint32, byte, []byte, error) {
	var h [9]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return 0, 0, nil, e
	}
	n := binary.LittleEndian.Uint32(h[5:])
	if n > 256*1024 {
		return 0, 0, nil, fmt.Errorf("observer frame too large")
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r, b)
	return binary.LittleEndian.Uint32(h[:]), h[4], b, e
}

type coreCell struct {
	r   rune
	dim bool
	bg  string
}

// This bounded first-paint decoder is intentionally not a live VT emulator.
// It accepts the positioning/erase/style subset emitted by Core's screen
// formatter, and refuses unsupported controls rather than guessing a draft.
func coreStyledComposer(paint []byte, cols, rows int, plain string) error {
	refuse := func(why string) error { return fmt.Errorf("%w: %s", ErrDraftOnPrompt, why) }
	if cols < 1 || cols > 500 || rows < 1 || rows > 300 || len(paint) == 0 || paint[0] != 0x18 {
		return refuse("unsupported observer paint/grid")
	}
	grid := make([][]coreCell, rows)
	for i := range grid {
		grid[i] = make([]coreCell, cols)
	}
	x, y, sx, sy := 0, 0, 0, 0
	dim := false
	bg := ""
	tabs := make([]bool, cols)
	for column := 8; column < cols; column += 8 {
		tabs[column] = true
	}
	moveTabs := func(direction, count int) {
		for step := 0; step < count; step++ {
			next := cols - 1
			if direction < 0 {
				next = 0
			}
			for column := x + direction; column >= 0 && column < cols; column += direction {
				if tabs[column] {
					next = column
					break
				}
			}
			if next == x {
				break
			}
			x = next
		}
	}
	cleared := false
	erase := func(row, from, to int) {
		for c := max(0, from); c < min(cols, to); c++ {
			grid[row][c] = coreCell{bg: bg}
		}
	}
	for i := 0; i < len(paint); {
		b := paint[i]
		if b == 0x1b {
			if i+1 >= len(paint) {
				return refuse("unfinished escape")
			}
			if paint[i+1] == ']' {
				end := i + 2
				for end < len(paint) && paint[end] != 7 && !(paint[end] == 0x1b && end+1 < len(paint) && paint[end+1] == '\\') {
					end++
				}
				if end == len(paint) {
					return refuse("unfinished OSC")
				}
				if paint[end] == 7 {
					i = end + 1
				} else {
					i = end + 2
				}
				continue
			}
			if paint[i+1] == 'H' {
				if x < 0 || x >= cols {
					return refuse("tabstop outside observer grid")
				}
				tabs[x] = true
				i += 2
				continue
			}
			if paint[i+1] == '7' {
				sx, sy = x, y
				i += 2
				continue
			}
			if paint[i+1] == '8' {
				x, y = sx, sy
				i += 2
				continue
			}
			if paint[i+1] != '[' {
				return refuse("unsupported escape")
			}
			end := i + 2
			for end < len(paint) && (paint[end] < 0x40 || paint[end] > 0x7e) {
				end++
			}
			if end == len(paint) {
				return refuse("unfinished CSI")
			}
			arg := string(paint[i+2 : end])
			final := paint[end]
			i = end + 1
			// Mode reports in Core first paint have no cell content. Reset, keyboard
			// mode and screen selection precede its authoritative positioned rows.
			if final == 'm' {
				var e error
				dim, e = coreObserverDim(arg, dim)
				if e != nil {
					return refuse(e.Error())
				}
				bg = coreObserverBackground(arg, bg)
				continue
			}
			if (final == 'h' || final == 'l') && strings.HasPrefix(arg, "?") {
				continue
			}
			if final == 'u' && strings.HasPrefix(arg, "=") {
				continue
			}
			if final == 'q' && strings.HasSuffix(arg, " ") {
				if _, e := strconv.Atoi(strings.TrimSpace(arg)); e != nil {
					return refuse("invalid cursor shape")
				}
				continue
			}
			if final == 'p' && arg == "!" {
				x, y, dim = 0, 0, false
				bg = ""
				continue
			}
			nums := []int{}
			for _, a := range strings.Split(arg, ";") {
				if a == "" {
					nums = append(nums, 0)
					continue
				}
				v, e := strconv.Atoi(a)
				if e != nil || v < 0 || v > 10000 {
					return refuse("invalid CSI parameter")
				}
				nums = append(nums, v)
			}
			n := 1
			if len(nums) > 0 && nums[0] > 0 {
				n = nums[0]
			}
			switch final {
			case 'H', 'f':
				y = n - 1
				x = 0
				if len(nums) > 1 && nums[1] > 0 {
					x = nums[1] - 1
				}
			case 'g':
				if len(nums) != 1 {
					return refuse("unsupported tab clear")
				}
				switch nums[0] {
				case 0:
					if x >= cols {
						return refuse("tabstop outside observer grid")
					}
					tabs[x] = false
				case 3:
					for column := range tabs {
						tabs[column] = false
					}
				default:
					return refuse("unsupported tab clear")
				}
			case 'I':
				moveTabs(1, n)
			case 'Z':
				moveTabs(-1, n)
			case 'G', '`':
				x = n - 1
			case 'd':
				y = n - 1
			case 'A':
				y -= n
			case 'B':
				y += n
			case 'C':
				x += n
			case 'D':
				x -= n
			case 'E':
				y += n
				x = 0
			case 'F':
				y -= n
				x = 0
			case 'J':
				switch nums[0] {
				case 2, 3:
					for r := range grid {
						erase(r, 0, cols)
					}
					cleared = true
				case 0:
					erase(y, x, cols)
					for r := y + 1; r < rows; r++ {
						erase(r, 0, cols)
					}
				case 1:
					for r := 0; r < y; r++ {
						erase(r, 0, cols)
					}
					erase(y, 0, x+1)
				default:
					return refuse("unsupported erase")
				}
			case 'K':
				switch nums[0] {
				case 0:
					erase(y, x, cols)
				case 1:
					erase(y, 0, x+1)
				case 2:
					erase(y, 0, cols)
				default:
					return refuse("unsupported line erase")
				}
			case 's':
				sx, sy = x, y
			case 'u':
				x, y = sx, sy
			case 'r':
				if !(arg == "" || arg == fmt.Sprintf("1;%d", rows)) {
					return refuse("unsupported scroll region")
				}
			default:
				return refuse("unsupported CSI")
			}
			if x < 0 || x > cols || y < 0 || y >= rows {
				return refuse("cursor outside observer grid")
			}
			continue
		}
		switch b {
		case 0x18, 7:
			i++
			continue
		case '\r':
			x = 0
			i++
			continue
		case '\n':
			y++
			if y >= rows {
				return refuse("observer paint scrolls")
			}
			i++
			continue
		case '\b':
			x = max(0, x-1)
			i++
			continue
		case '\t':
			moveTabs(1, 1)
			i++
			continue
		}
		if b < 0x20 || b == 0x7f {
			return refuse("unsupported control")
		}
		r, size := utf8.DecodeRune(paint[i:])
		if r == utf8.RuneError && size == 1 {
			return refuse("invalid observer UTF-8")
		}
		i += size
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			continue
		}
		width := 1
		if r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe6f || r >= 0xff01 && r <= 0xff60 || r >= 0x1f000) {
			width = 2
		}
		if x+width > cols {
			x = 0
			y++
			if y >= rows {
				return refuse("observer paint wraps outside grid")
			}
		}
		grid[y][x] = coreCell{r: r, dim: dim, bg: bg}
		for n := 1; n < width; n++ {
			grid[y][x+n] = coreCell{r: ' ', dim: dim, bg: bg}
		}
		x += width
	}
	if !cleared {
		return refuse("observer paint lacks authoritative clear")
	}
	if x < 0 || x >= cols || y < 0 || y >= rows {
		return refuse("observer cursor outside composer grid")
	}
	ink := func(c coreCell) bool { return c.r != 0 && c.r != ' ' }
	firstInk := func(row []coreCell) int {
		for c, cell := range row {
			if ink(cell) {
				return c
			}
		}
		return -1
	}
	markerRow, markerCol, regionEnd := -1, -1, rows-1
	composerBG := grid[y][x].bg
	if composerBG != "" {
		// Codex paints a rectangular composer surface, including its blank
		// padding rows. Bound that whole surface before looking for its prompt;
		// a rune typed inside the content cannot redefine the boundary.
		inSurface := func(r int) bool {
			for _, cell := range grid[r] {
				if cell.bg != composerBG {
					return false
				}
			}
			return true
		}
		if !inSurface(y) {
			return refuse("observer composer surface is incomplete")
		}
		top := y
		for top > 0 && inSurface(top-1) {
			top--
		}
		regionEnd = y
		for regionEnd+1 < rows && inSurface(regionEnd+1) {
			regionEnd++
		}
		for r := top; r <= regionEnd; r++ {
			if c := firstInk(grid[r]); c >= 0 {
				markerRow, markerCol = r, c
				break
			}
		}
	} else {
		// A minimal uncolored paint is usable only with one structural prompt
		// and no content below it. Without a surface boundary, a footer or a
		// second prompt-looking row is ambiguous rather than empty input.
		for r, row := range grid {
			if c := firstInk(row); c >= 0 && row[c].r == '›' {
				if markerRow >= 0 {
					return refuse("observer has ambiguous uncolored prompts")
				}
				markerRow, markerCol = r, c
			}
		}
	}
	if markerRow < 0 || grid[markerRow][markerCol].r != '›' ||
		y != markerRow || x != markerCol+2 {
		return refuse("observer cursor does not identify an empty composer")
	}
	for r := markerRow; r <= regionEnd; r++ {
		for c, cell := range grid[r] {
			if r == markerRow && c == markerCol {
				continue
			}
			if ink(cell) && (r != markerRow || !cell.dim) {
				return ErrDraftOnPrompt
			}
		}
	}
	rowText := func(row []coreCell, hideDim bool) string {
		var b strings.Builder
		for _, c := range row {
			r := c.r
			if r == 0 {
				r = ' '
			}
			if hideDim && c.dim && r != '›' {
				r = ' '
			}
			b.WriteRune(r)
		}
		return strings.TrimSpace(b.String())
	}
	pi := coreComposerStart(plain)
	if pi < 0 {
		return refuse("plain capture has no composer")
	}
	plainRows := strings.Split(plain[pi:], "\n")
	compareEnd := regionEnd
	if composerBG == "" {
		compareEnd = markerRow
	}
	for r := markerRow; r <= compareEnd; r++ {
		line := ""
		if i := r - markerRow; i < len(plainRows) {
			line = strings.TrimSpace(plainRows[i])
		}
		if line != rowText(grid[r], false) {
			return refuse("plain and styled composer disagree")
		}
	}
	observed := rowText(grid[markerRow][markerCol:], false)
	ghost := strings.TrimSpace(strings.TrimPrefix(observed, "›"))
	known := ghost == "" || strings.HasPrefix(ghost, "Try \"") || ghost == "Ask Codex to do anything" || ghost == "Ask a follow-up question"
	if !known {
		return refuse("unknown dim composer content")
	}
	return nil
}

// SGR background and underline colors may contain numeric 2, 22 or a
// foreground-color code as RGB components. They never change faintness.
// Keep those groups out of the shared foreground/dim interpreter.
func coreObserverDim(params string, dim bool) (bool, error) {
	if params == "" {
		return false, nil
	}
	p := strings.Split(params, ";")
	foreground := []string{}
	for i := 0; i < len(p); i++ {
		if p[i] == "" {
			foreground = append(foreground, "0")
			continue
		}
		n, e := strconv.Atoi(p[i])
		if e != nil {
			return dim, fmt.Errorf("unsupported observer SGR")
		}
		if n == 38 || n == 48 || n == 58 {
			if i+1 >= len(p) {
				return dim, fmt.Errorf("incomplete observer color")
			}
			count := 0
			switch p[i+1] {
			case "5":
				count = 2
			case "2":
				count = 4
			default:
				return dim, fmt.Errorf("unsupported observer color")
			}
			if i+count >= len(p) {
				return dim, fmt.Errorf("incomplete observer color")
			}
			for _, component := range p[i+2 : i+count+1] {
				value, e := strconv.Atoi(component)
				if e != nil || value < 0 || value > 255 {
					return dim, fmt.Errorf("invalid observer color")
				}
			}
			if n == 38 {
				foreground = append(foreground, p[i:i+count+1]...)
			}
			i += count
			continue
		}
		foreground = append(foreground, p[i])
	}
	if len(foreground) == 0 {
		return dim, nil
	}
	return sgrDim(strings.Join(foreground, ";"), dim), nil
}

// Called only after coreObserverDim has validated the SGR groups. Keep the
// background identity so composer padding cannot be confused with its footer.
func coreObserverBackground(params, bg string) string {
	p := strings.Split(params, ";")
	for i := 0; i < len(p); i++ {
		n, _ := strconv.Atoi(p[i])
		switch n {
		case 0, 49:
			bg = ""
		case 40, 41, 42, 43, 44, 45, 46, 47, 100, 101, 102, 103, 104, 105, 106, 107:
			bg = p[i]
		case 38, 48, 58:
			count := 2
			if p[i+1] == "2" {
				count = 4
			}
			if n == 48 {
				bg = strings.Join(p[i:i+count+1], ";")
			}
			i += count
		}
	}
	return bg
}
