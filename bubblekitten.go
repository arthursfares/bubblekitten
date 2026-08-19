package bubblekitten

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"image"
	"image/png"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// idCounter hands out 24-bit image ids (the range that fits directly into a
// truecolor foreground SGR color, so placeholder cells never need the third
// "most significant byte" diacritic). It's seeded randomly so that
// independent programs sharing a terminal or multiplexer are unlikely to
// collide.
var idCounter uint32

func init() {
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err == nil {
		atomic.StoreUint32(&idCounter, binary.BigEndian.Uint32(seed[:])&0x00FFFFFF)
	} else {
		atomic.StoreUint32(&idCounter, 1)
	}
}

func nextImageID() uint32 {
	for {
		id := atomic.AddUint32(&idCounter, 1) & 0x00FFFFFF
		if id != 0 { return id }
	}
}

// pngEncodedMsg carries the result of asynchronously PNG-encoding an
// image. id correlates it back to the SetImage call that triggered it, so
// a stale result from a superseded image is ignored.
type pngEncodedMsg struct {
	id  uint32
	png []byte
	err error
}

// altScreenSyncDelay is how long SetAltScreen waits before actually
// retransmitting the image for the new screen context.
//
// Bubble Tea v2 doesn't flush a render as soon as Update returns: both the
// renderer's diffed screen paint (which is what writes the 1049h/1049l
// screen-switch sequence) and this component's raw image bytes (sent via
// tea.Raw) are buffered and only written out by a periodic ticker — 60
// times a second by default. On every tick, the buffered raw output is
// always flushed before the renderer's paint, regardless of which was
// buffered first. So if the retransmit is issued in the same ~16ms tick
// window as the AltScreen flip, its bytes reliably reach the terminal
// *before* the screen switch does, which creates the image's placement on
// the screen buffer being left rather than the one being entered —
// placements are per-screen-buffer, so the placeholder cells then
// reference a placement that doesn't exist on the new screen, and the
// image never appears. There's no public API to wait for an actual flush,
// so instead we wait comfortably longer than one frame period at any
// realistic frame rate, which guarantees the screen switch has already
// been flushed on an earlier tick before we send the retransmit.
const altScreenSyncDelay = 50 * time.Millisecond

// altScreenSyncMsg triggers the actual retransmit/placement update after an
// AltScreen transition, once altScreenSyncDelay has given the screen switch
// time to reach the terminal first. See altScreenSyncDelay for why this
// can't just happen immediately.
type altScreenSyncMsg struct {
	id        uint32
	altScreen bool
}

// transmitCompleteMsg confirms that every chunk of a full image transmission
// (see rawBatch) has actually been queued for output. Only once this is
// received does Update mark the image transmitted for its context, which is
// what lets View start printing placeholder cells for it.
//
// Marking it transmitted as soon as the transmit command is merely created,
// rather than once its chunks are confirmed queued, races the same way the
// AltScreen switch did (see altScreenSyncDelay): a large image can take many
// chunks — each its own message round trip, batched across several render
// ticks — to fully reach the terminal. If View started printing placeholder
// cells the moment the first chunk went out, it would reference an image
// the terminal hasn't fully received yet, and — because Bubble Tea's
// renderer doesn't redraw content that hasn't changed between frames —
// nothing would ever prompt a repaint once the rest of the data arrived.
type transmitCompleteMsg struct {
	id         uint32
	altScreen  bool
	cols, rows int
}

// ErrMsg is emitted (via a command from Update) when encoding an image
// fails. Bubble Tea programs can watch for it if they want to surface
// errors to the user; the component itself just quietly renders nothing.
type ErrMsg struct{ Err error }

func (e ErrMsg) Error() string { return e.Err.Error() }

// Model is a Bubbles-style component that displays a single image via the
// Kitty graphics protocol's Unicode placeholder mechanism. The zero value
// is not usable; construct with New.
type Model struct {
	id      uint32
	support Support

	cols, rows int 				// requested display size, in terminal cells

	currentPNG []byte 			// most recently encoded PNG bytes for id
	pending    bool   			// an encode is in flight

	transmittedMain bool 		// image data has been sent for the main-screen context
	transmittedAlt  bool 		// image data has been sent for the alt-screen context
	altScreen       bool 		// last AltScreen state we were told about

	placedCols, placedRows int 	// size the terminal-side placement currently reflects

	err error
}

// New creates an empty Model. Call SetImage to give it something to
// display and SetSize to tell it how many terminal cells to fill.
func New() Model {
	return Model{
		id:      nextImageID(),
		support: DetectSupport(),
	}
}

// Option configures a Model at construction time.
type Option func(*Model)

// WithSupport overrides automatic terminal-capability detection. Use this
// if you've already determined support some other way (a config flag, a
// prior successful render, etc.).
func WithSupport(s Support) Option {
	return func(m *Model) { m.support = s }
}

// NewWithOptions creates a Model with the given options applied.
func NewWithOptions(opts ...Option) Model {
	m := New()
	for _, opt := range opts { opt(&m) }
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

// SetImage sets (or replaces) the image to display and kicks off
// asynchronous PNG encoding. The returned command must be added to your
// Update's outgoing commands. Replacing the image assigns it a fresh
// terminal-side id and invalidates any previous transmission; the old
// image's data is also deleted from the terminal to free its storage
// quota.
func (m Model) SetImage(img image.Image) (Model, tea.Cmd) {
	oldID := m.id
	hadImage := len(m.currentPNG) > 0 || m.pending

	m.id = nextImageID()
	m.currentPNG = nil
	m.pending = true
	m.transmittedMain = false
	m.transmittedAlt = false
	m.placedCols, m.placedRows = 0, 0
	m.err = nil

	newID := m.id
	encodeCmd := func() tea.Msg {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil { return pngEncodedMsg{id: newID, err: err} }
		return pngEncodedMsg{id: newID, png: buf.Bytes()}
	}
	cmds := []tea.Cmd{encodeCmd}
	if hadImage { cmds = append([]tea.Cmd{deleteCmd(oldID)}, cmds...) }
	return m, tea.Batch(cmds...)
}

// SetSize sets the display size in terminal cells (not pixels). The image
// is scaled to fit while preserving aspect ratio by the terminal itself,
// via the protocol's c/r keys. Call this from your tea.WindowSizeMsg
// handling (or whenever your layout changes). If the size actually changes
// and the image is already on the terminal for the current context, only a
// cheap placement update is sent — the pixel payload is not retransmitted.
func (m Model) SetSize(cols, rows int) (Model, tea.Cmd) {
	if cols == m.cols && rows == m.rows { return m, nil }
	m.cols, m.rows = cols, rows
	return m.maybeTransmitOrReplace()
}

// SetAltScreen tells the component whether the program is currently
// rendering to the terminal's alternate screen buffer. Call this from your
// own Update method at the same point you change the AltScreen field you
// return from View — View itself must stay pure, so this transition can't
// be inferred automatically.
//
// Per the Kitty graphics protocol spec, entering the alternate screen
// clears every image that was placed on it, exactly like it clears text.
// This method is what makes the component notice that and automatically
// retransmit, which is the fix for images "disappearing" after toggling or
// re-entering AltScreen.
func (m Model) SetAltScreen(active bool) (Model, tea.Cmd) {
	if active == m.altScreen { return m, nil }
	m.altScreen = active
	if active {
		// The terminal just cleared (or is about to clear, on the next
		// 1049h) the alt screen's images. Whatever we sent there before
		// is gone and must be resent.
		m.transmittedAlt = false
		m.placedCols, m.placedRows = 0, 0
	}
	// Don't call maybeTransmitOrReplace here: see altScreenSyncDelay for why
	// issuing the retransmit immediately would race the renderer's own
	// 1049h/1049l write.
	id := m.id
	return m, tea.Tick(altScreenSyncDelay, func(time.Time) tea.Msg {
		return altScreenSyncMsg{id: id, altScreen: active}
	})
}

// Update handles the asynchronous result of SetImage's PNG encoding. Wire
// it into your program's Update like any other Bubbles component.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case pngEncodedMsg:
		if msg.id != m.id {
			return m, nil // superseded by a newer SetImage call
		}
		m.pending = false
		if msg.err != nil {
			m.err = msg.err
			return m, func() tea.Msg { return ErrMsg{Err: msg.err} }
		}
		m.currentPNG = msg.png
		return m.maybeTransmitOrReplace()
	case altScreenSyncMsg:
		if msg.id != m.id || msg.altScreen != m.altScreen {
			return m, nil // superseded by a newer SetImage or SetAltScreen call
		}
		return m.maybeTransmitOrReplace()
	case transmitCompleteMsg:
		if msg.id != m.id || msg.altScreen != m.altScreen || msg.cols != m.cols || msg.rows != m.rows {
			return m, nil // superseded by a newer SetImage, SetSize, or SetAltScreen call
		}
		if msg.altScreen {
			m.transmittedAlt = true
		} else {
			m.transmittedMain = true
		}
		m.placedCols, m.placedRows = msg.cols, msg.rows
		return m, nil
	}
	return m, nil
}

// maybeTransmitOrReplace decides, given the current cols/rows, AltScreen
// context, and whether we already have PNG bytes, whether to:
//
//   - do nothing (nothing to show yet, or nothing changed),
//   - send a full transmit (new image data, or a screen context we
//     haven't sent to yet — e.g. AltScreen was just (re-)entered), or
//   - send a cheap placement-only update (size changed but the terminal
//     already has our pixel data for this context).
func (m Model) maybeTransmitOrReplace() (Model, tea.Cmd) {
	if m.support == SupportNo { return m, nil }
	if len(m.currentPNG) == 0 || m.cols <= 0 || m.rows <= 0 { return m, nil }

	transmittedThisContext := m.transmittedMain
	if m.altScreen { transmittedThisContext = m.transmittedAlt }

	sizeChanged := m.placedCols != m.cols || m.placedRows != m.rows

	switch {
	case !transmittedThisContext:
		cmds := buildTransmitCommands(m.id, m.currentPNG, m.cols, m.rows)
		id, altScreen, cols, rows := m.id, m.altScreen, m.cols, m.rows
		done := func() tea.Msg {
			return transmitCompleteMsg{id: id, altScreen: altScreen, cols: cols, rows: rows}
		}
		// done is sequenced after rawBatch(cmds) rather than flipping the
		// transmitted flag here directly — see transmitCompleteMsg for why.
		return m, tea.Sequence(rawBatch(cmds), done)
	case sizeChanged:
		m.placedCols, m.placedRows = m.cols, m.rows
		return m, tea.Raw(buildPlacementUpdate(m.id, m.cols, m.rows))
	default:
		return m, nil
	}
}

// rawBatch wraps a slice of raw escape sequences as a single, in-order
// command, each sent via tea.Raw so it bypasses Bubble Tea's styled,
// width-measured render pipeline entirely. Order matters — chunked
// transmissions must arrive at the terminal in sequence — so this uses
// tea.Sequence rather than tea.Batch.
func rawBatch(seqs []string) tea.Cmd {
	cmds := make([]tea.Cmd, len(seqs))
	for i, s := range seqs {
		cmds[i] = tea.Raw(s)
	}
	return tea.Sequence(cmds...)
}

// deleteCmd frees a previously-transmitted image's terminal-side storage.
func deleteCmd(id uint32) tea.Cmd {
	return tea.Raw(buildDelete(id))
}

// View renders the placeholder grid for the current image at its last-set
// size. It contains no raw Kitty escape codes — only standard SGR color
// sequences and ordinary (if unusual) runes — so it composes safely inside
// Lip Gloss layouts, wraps and diffs like normal text, and survives
// AltScreen redraws. If no image is set, size is zero, or the terminal is
// known not to support the protocol, it renders an empty string.
func (m Model) View() string {
	if !m.Ready() {
		// Either there's nothing to show yet, or we have the pixel data
		// but haven't (yet) placed it on the screen buffer currently in
		// effect — most commonly, immediately after an AltScreen
		// transition, before altScreenSyncMsg has come back around to
		// trigger the retransmit. Render nothing rather than placeholder
		// cells with no backing placement.
		return ""
	}
	var b bytes.Buffer
	for row := 0; row < m.rows; row++ {
		for col := 0; col < m.cols; col++ {
			b.WriteString(placeholderCell(m.id, row, col))
		}
		b.WriteString(resetFG)
		if row < m.rows-1 { b.WriteByte('\n') }
	}
	return b.String()
}

// Supported reports whether the terminal is known not to support the kitty
// graphics protocol (SupportNo) - i.e. whether View will ever render
// anything for this Model, as opposed to Ready, which reports whether it
// has yet. Callers that want to show a fallback instead of waiting on an
// image that will never arrive can check this once, right after New.
func (m Model) Supported() bool { return m.support != SupportNo }

// Ready reports whether the image has been fully transmitted and placed for
// the screen context (main vs AltScreen) currently in effect — i.e. whether
// View will render actual placeholder cells rather than an empty string.
// Callers that embed View's output inside other content (so they need to
// know when that content has changed) can poll this after each Update.
func (m Model) Ready() bool {
	if m.support == SupportNo || m.cols <= 0 || m.rows <= 0 || len(m.currentPNG) == 0 { return false }
	if m.altScreen { return m.transmittedAlt }
	return m.transmittedMain
}

// Err returns the last image-encoding error, if any.
func (m Model) Err() error { return m.err }

// ID returns the Kitty graphics protocol image id currently assigned to
// this component. Mostly useful for debugging or manual protocol
// interaction (e.g. issuing your own delete command on shutdown).
func (m Model) ID() uint32 { return m.id }

// Close returns a command that deletes the component's image from the
// terminal, freeing its storage quota. Call it when you're done with the
// component (e.g. on tea.Quit) if you're not replacing it with SetImage,
// which already cleans up after itself.
func (m Model) Close() tea.Cmd {
	if len(m.currentPNG) == 0 && !m.pending { return nil }
	return deleteCmd(m.id)
}
