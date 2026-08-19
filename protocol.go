package bubblekitten

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// placeholderRune is the Private Use codepoint (U+10EEEE) that the Kitty
// graphics protocol treats as "an image tile is displayed here" when it
// appears in the normal text stream. See:
// https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders
const placeholderRune = rune(0x10EEEE)

// chunkSize is the maximum size, in bytes, of a base64-encoded payload
// chunk per escape code, per spec ("chunks no larger than 4096 bytes").
// It must be a multiple of 4 (base64 group size) for every non-final chunk.
const chunkSize = 4096

const resetFG = "\x1b[39m"

// buildTransmitCommands base64-encodes pngData and splits it into a series
// of "<ESC>_G...;<payload><ESC>\" APC escape codes that transmit the image
// and, on the very first chunk, create a virtual placement (U=1) sized
// cols x rows. Because U=1 placements are invisible until a placeholder
// referencing the image id is printed, this is always safe to send eagerly.
//
// q=2 is used (per the protocol docs' recommendation for the placeholder
// workflow) so the terminal does not send back OK/error acknowledgements
// that could otherwise be misinterpreted as ordinary input by the host
// application's event loop.
func buildTransmitCommands(id uint32, pngData []byte, cols, rows int) []string {
	encoded := base64.StdEncoding.EncodeToString(pngData)

	var chunks []string
	for len(encoded) > 0 {
		n := chunkSize
		if n > len(encoded) { n = len(encoded) }
		chunks = append(chunks, encoded[:n])
		encoded = encoded[n:]
	}
	if len(chunks) == 0 { chunks = []string{""} }

	cmds := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		more := i < len(chunks)-1
		var b strings.Builder
		b.WriteString("\x1b_G")
		if i == 0 {
			b.WriteString("a=T,f=100,t=d,q=2,U=1,")
			b.WriteString("i=")
			b.WriteString(strconv.FormatUint(uint64(id), 10))
			b.WriteString(",c=")
			b.WriteString(strconv.Itoa(cols))
			b.WriteString(",r=")
			b.WriteString(strconv.Itoa(rows))
			b.WriteString(",")
		}
		if more {
			b.WriteString("m=1")
		} else {
			b.WriteString("m=0")
		}
		b.WriteString(";")
		b.WriteString(chunk)
		b.WriteString("\x1b\\")
		cmds = append(cmds, b.String())
	}
	return cmds
}

// buildPlacementUpdate re-creates the virtual placement for an
// already-transmitted image at a new size, without resending any pixel
// data. Use this on resize instead of a full retransmit.
func buildPlacementUpdate(id uint32, cols, rows int) string {
	return fmt.Sprintf("\x1b_Ga=p,q=2,U=1,i=%d,c=%d,r=%d\x1b\\", id, cols, rows)
}

// buildDelete removes an image (and its data) by id, freeing terminal-side
// storage quota. Call this when swapping images or tearing down the model.
func buildDelete(id uint32) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,q=2,i=%d\x1b\\", id)
}

// buildSupportQuery returns an escape sequence that, combined with a
// request for the primary device attributes, can be used to detect
// graphics-protocol support: a terminal that replies to the graphics query
// supports the protocol, one that only replies to the DA1 request does not.
// See "Querying support..." in the protocol docs. Note that consuming the
// response requires access to raw terminal input, which the default
// Bubble Tea input loop does not expose; this is provided for callers that
// have their own raw-input hook.
func buildSupportQuery() string {
	return "\x1b_Gi=1,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"
}

// placeholderCell renders one cell of the Unicode-placeholder image grid:
// a foreground color (encoding the 24-bit image id) followed by the
// placeholder rune and its row/column diacritics. Row and column diacritics
// are always emitted explicitly (rather than relying on the terminal's
// "inherit from the cell to the left" fallback) so that the grid renders
// correctly even if Bubble Tea's renderer redraws individual lines
// independently.
func placeholderCell(id uint32, row, col int) string {
	r := byte(id >> 16)
	g := byte(id >> 8)
	b := byte(id)
	var out strings.Builder
	out.WriteString("\x1b[38;2;")
	out.WriteString(strconv.Itoa(int(r)))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(int(g)))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(int(b)))
	out.WriteByte('m')
	out.WriteRune(placeholderRune)
	out.WriteRune(diacritic(row))
	out.WriteRune(diacritic(col))
	return out.String()
}

// diacritic returns the combining mark encoding index i (a row or column
// number) per kitty's canonical rowcolumn-diacritics.txt table. Indices
// beyond the table wrap, which only matters for images larger than 297
// rows/cols — far beyond what fits in any real terminal.
func diacritic(i int) rune {
	return diacritics[i%len(diacritics)]
}
