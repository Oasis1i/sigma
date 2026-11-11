package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Styles stay in one place so the view function reads as layout rather than
// as a list of color codes. The palette is a dark terminal: amber for the
// assistant, blue for the user, and the usual diff colors.
var (
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("236"))
	approvalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("94"))
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	sigmaStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("215")).Bold(true)
	noticeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	toolStyle   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
	diffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	diffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	diffMeta = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	faint    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func (m *Model) layout() {
	if m.width < 1 {
		m.width = 80
	}
	if m.height < 1 {
		m.height = 24
	}
	approvalH := 0
	if m.pending != nil {
		approvalH = 2
	}
	const statusH = 1
	const inputH = 3
	vpH := m.height - statusH - approvalH - inputH
	if vpH < 1 {
		vpH = 1
	}
	m.vp.Width = m.width
	m.vp.Height = vpH
	m.ta.SetWidth(m.width)
	m.ta.SetHeight(inputH)
	m.ready = true
	m.syncViewport()
}

func (m *Model) syncViewport() {
	if m.width < 1 {
		return
	}
	stick := m.follow || m.vp.AtBottom()
	m.vp.SetContent(m.renderTranscript())
	if stick {
		m.vp.GotoBottom()
		m.follow = true
	}
}

func (m *Model) View() string {
	if !m.ready {
		return "starting sigma…"
	}
	parts := []string{m.vp.View(), m.statusBar()}
	if m.pending != nil {
		parts = append(parts, m.approvalView())
	}
	parts = append(parts, m.ta.View())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) statusBar() string {
	total := m.promptTokens + m.completionTokens
	tok := "tok —"
	if total > 0 {
		tok = "tok " + formatTokens(total)
	}
	step := "step —"
	if m.maxStep > 0 {
		step = fmt.Sprintf("step %d/%d", m.step, m.maxStep)
	}
	id := m.sessionID
	if id == "" {
		id = "no session"
	}
	left := fmt.Sprintf(" %s  %s  %s  %s  %s", emptyDash(m.modelName), emptyDash(m.mode), step, tok, id)
	right := shortenPath(m.wsPath)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return statusStyle.Width(m.width).Render(fit(left, m.width))
	}
	line := left + strings.Repeat(" ", gap) + " " + right
	return statusStyle.Width(m.width).Render(fit(line, m.width))
}

func (m *Model) approvalView() string {
	arg := oneLine(m.pending.Args, max(8, m.width-len(m.pending.Tool)-12))
	line1 := fit(" Allow "+m.pending.Tool+"?  "+arg, m.width)
	line2 := fit(" y yes    n no    a always    esc deny", m.width)
	return approvalStyle.Width(m.width).Render(line1 + "\n" + line2)
}

func (m *Model) renderTranscript() string {
	width := m.vp.Width
	if width < 8 {
		width = m.width
	}
	if width < 8 {
		width = 80
	}
	inner := width - 2
	if inner < 8 {
		inner = 8
	}
	var b strings.Builder
	for i := range m.blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.renderBlock(&m.blocks[i], inner))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *Model) renderBlock(blk *block, width int) string {
	switch blk.kind {
	case blockUser:
		return userStyle.Render("you") + "\n" + wrap(blk.text, width)
	case blockAssistant:
		return sigmaStyle.Render("sigma") + "\n" + m.renderMarkdown(blk, width)
	case blockNotice:
		return noticeStyle.Render(wrap(blk.text, width))
	case blockError:
		return errorStyle.Render(wrap(blk.text, width))
	case blockTool:
		return m.renderTool(blk, width)
	default:
		return blk.text
	}
}

func (m *Model) renderMarkdown(blk *block, width int) string {
	// Streaming tokens are drawn as plain text. Glamour on every delta is
	// slow, and a half-written code fence looks worse than raw text. The
	// finished message is rendered once and cached for this width.
	if blk.streaming {
		return wrap(blk.text, width)
	}
	if blk.rendered != "" && blk.renderFor == width {
		return blk.rendered
	}
	out, err := m.markdown(width, blk.text)
	if err != nil || strings.TrimSpace(out) == "" {
		blk.rendered = wrap(blk.text, width)
	} else {
		blk.rendered = strings.TrimRight(out, "\n")
	}
	blk.renderFor = width
	return blk.rendered
}

func (m *Model) renderTool(blk *block, width int) string {
	mark := "▼"
	if blk.collapsed {
		mark = "▶"
	}
	title := mark + " " + blk.tool
	if blk.preview != "" {
		title += "  " + oneLine(blk.preview, 72)
	}
	switch {
	case blk.running:
		title += "  " + faint.Render("running")
	case blk.failed:
		title += "  " + errorStyle.Render("failed")
	}
	body := title
	if !blk.collapsed {
		var parts []string
		if blk.diff != "" {
			parts = append(parts, colorDiff(blk.diff))
		}
		if blk.output != "" {
			parts = append(parts, capLines(blk.output, 80))
		} else if blk.running {
			parts = append(parts, faint.Render("…"))
		}
		if len(parts) > 0 {
			body += "\n" + strings.Join(parts, "\n")
		}
	}
	boxWidth := width
	if boxWidth > 2 {
		boxWidth -= 1
	}
	return toolStyle.Width(boxWidth).Render(body)
}

func colorDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			lines[i] = diffAdd.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = diffDel.Render(line)
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			lines[i] = diffMeta.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func wrap(s string, width int) string {
	if width < 1 || s == "" {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

func capLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-n)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	return fit(s, n)
}

func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return string(r[:1])
	}
	return string(r[:width-1]) + "…"
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

func shortenPath(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if lipgloss.Width(path) <= 28 {
		return path
	}
	short := filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path))
	if lipgloss.Width(short) <= 28 {
		return short
	}
	return fit(filepath.Base(path), 28)
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
