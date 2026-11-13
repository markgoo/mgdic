package winui

import (
	"runtime"
	"strings"
)

// 结果区是两层：
//
//	┌──────────────┐
//	│ 主翻译        │  ← 自己画的圆角框，点一下复制这一块
//	└──────────────┘
//	音标 · 释义 · 考纲 · 词形     ← RichEdit 只读文本框，可以拖选其中一段复制
//
// 框负责"一眼看到答案、一点就复制"；文本框负责"想挑其中几个字就挑"。

const (
	boxPadX   = 14
	boxPadY   = 10
	boxGap    = 8
	boxRadius = 10
	boxIndent = 0
	boxTextGap = 10 // 框区与下方文本框之间
)

// boxAreaHeight 算出框区（主翻译）需要多高。
func boxAreaHeight(hdc uintptr, l layout) int {
	if len(app.result.Cards) == 0 {
		return 0
	}
	width := l.resW()
	h := 0
	for i, c := range app.result.Cards {
		if i > 0 {
			h += boxGap
		}
		h += boxPadY*2 + 2
		for _, line := range c.Lines {
			hh := measureText(hdc, line, width-2*boxPadX, app.fontSm)
			if hh <= 0 {
				hh = 16
			}
			h += hh
		}
	}
	return h + boxTextGap
}

// paintBoxes 画主翻译的圆角框，并缓存矩形给点击命中用。
func paintBoxes(hdc uintptr, l layout) {
	app.boxRects = app.boxRects[:0]
	if len(app.result.Cards) == 0 {
		return
	}
	procSetBkMode.Call(hdc, bkTransparent)
	y := l.resY()
	width := l.resW()
	for i, c := range app.result.Cards {
		if i > 0 {
			y += boxGap
		}
		h := boxPadY*2 + 2
		hs := make([]int, len(c.Lines))
		for j, line := range c.Lines {
			hs[j] = measureText(hdc, line, width-2*boxPadX, app.fontSm)
			if hs[j] <= 0 {
				hs[j] = 16
			}
			h += hs[j]
		}
		r := rect{
			Left:   int32(l.resX()),
			Top:    int32(y),
			Right:  int32(l.resX() + width),
			Bottom: int32(y + h),
		}
		app.boxRects = append(app.boxRects, hitTarget{r: r, text: c.Copy})

		bg := th.CardBg
		if i == app.hoverBox {
			bg = th.CardHover
		}
		drawRoundRectAA(hdc, int(r.Left), int(r.Top), width, h, boxRadius, bg)
		drawRoundBorder(hdc, r, boxRadius, th.CardBorder)

		procSetTextColor.Call(hdc, uintptr(th.Text))
		if app.fontSm != 0 {
			procSelectObject.Call(hdc, app.fontSm)
		}
		yy := y + boxPadY
		for j, line := range c.Lines {
			drawLine(hdc, line, int(r.Left)+boxPadX, yy, width-2*boxPadX)
			yy += hs[j]
		}
		y += h
	}
}

// hitBox 返回坐标落在哪个圆角框上，没命中返回 -1。
func hitBox(x, y int) int {
	for i, b := range app.boxRects {
		r := b.r
		if x >= int(r.Left) && x < int(r.Right) && y >= int(r.Top) && y < int(r.Bottom) {
			return i
		}
	}
	return -1
}

// infoText 是下方文本框要显示的内容：每个词条的辅助信息 + 提示/译文。
func infoText(r Result) string {
	var b strings.Builder
	for _, c := range r.Cards {
		var parts []string
		if c.Title != "" {
			parts = append(parts, c.Title)
		}
		if c.Meta != "" {
			parts = append(parts, c.Meta)
		}
		if len(parts) > 0 {
			b.WriteString(strings.Join(parts, "  ·  "))
			b.WriteString("\n")
		}
	}
	if len(r.Chips) > 0 {
		b.WriteString(strings.Join(r.Chips, "   "))
		b.WriteString("\n")
	}
	if r.Note != "" {
		b.WriteString(r.Note)
	}
	return strings.TrimRight(b.String(), "\n")
}

// measureText 用 DrawText 量出一段文字在给定宽度下需要多高。
func measureText(hdc uintptr, text string, width int, font handle) int {
	if text == "" || width <= 0 {
		return 0
	}
	rc := rect{Right: int32(width)}
	t := utf16(text)
	if len(t) == 0 {
		return 0
	}
	if font != 0 {
		procSelectObject.Call(hdc, font)
	}
	procDrawText.Call(hdc, pptr(&t[0]), ^uintptr(0), pptr(&rc),
		dtLeft|dtWordBreak|dtNoPrefix|dtCalcRect)
	runtime.KeepAlive(&t)
	runtime.KeepAlive(&rc)
	return int(rc.Bottom - rc.Top)
}

// drawLine 在 (x,y) 处画一段可换行的文字，返回它占的高度。
func drawLine(hdc uintptr, text string, x, y, width int) int {
	if text == "" || width <= 0 {
		return 0
	}
	h := measureText(hdc, text, width, 0)
	if h <= 0 {
		h = 16
	}
	rc := rect{Left: int32(x), Top: int32(y), Right: int32(x + width), Bottom: int32(y + h)}
	t := utf16(text)
	if len(t) == 0 {
		return 0
	}
	procDrawText.Call(hdc, pptr(&t[0]), ^uintptr(0), pptr(&rc),
		dtLeft|dtWordBreak|dtNoPrefix)
	runtime.KeepAlive(&t)
	runtime.KeepAlive(&rc)
	return h + 2
}


// drawRoundBorder 只描边不填色。
func drawRoundBorder(hdc uintptr, r rect, radius int, color uint32) {
	pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(color))
	if pen == 0 {
		return
	}
	old, _, _ := procSelectObject.Call(hdc, pen)
	nb, _, _ := procGetStockObject.Call(nullBrush)
	oldBr, _, _ := procSelectObject.Call(hdc, nb)
	rad := uintptr(radius * 2)
	procRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top),
		uintptr(r.Right), uintptr(r.Bottom), rad, rad)
	procSelectObject.Call(hdc, oldBr)
	procSelectObject.Call(hdc, old)
	procDeleteObject.Call(pen)
}
// layoutResult 按主翻译框的高度，把下方的只读文本框摆到它下面。
func layoutResult() {
	l := layoutFor(app.dpi)
	hdc, _, _ := procGetDC.Call(app.hwnd)
	boxH := 0
	if hdc != 0 {
		if app.fontSm != 0 {
			procSelectObject.Call(hdc, app.fontSm)
		}
		boxH = boxAreaHeight(hdc, l)
		procReleaseDC.Call(app.hwnd, hdc)
	}
	y := l.resY() + boxH
	h := l.resH() - boxH
	if h < 24 {
		h = 24
	}
	call(procSetPos, app.hResult, 0,
		uintptr(l.resX()), uintptr(y), uintptr(l.resW()), uintptr(h),
		0x0004|0x0010|0x0020)
}