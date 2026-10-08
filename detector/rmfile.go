package main

// A reader for the parts of reMarkable's v6 page format ("reMarkable .lines file, version=6")
// that detection needs: pen strokes, the anchor x of the group each stroke sits in, and the
// y position of the page's root text block. Everything else is skipped by length.
// Ported from rmscene (github.com/ricklupton/rmscene).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const headerV6 = "reMarkable .lines file, version=6          "

const (
	tagID      = 0xF
	tagLength4 = 0xC
	tagByte8   = 0x8
	tagByte4   = 0x4
	tagByte1   = 0x1

	blockTreeNode = 0x02
	blockLineItem = 0x05
	blockRootText = 0x07
)

type crdtID struct {
	part1 uint8
	part2 uint64
}

func (c crdtID) String() string { return fmt.Sprintf("%d.%d", c.part1, c.part2) }

type point struct{ x, y float64 }

type line struct {
	id     crdtID
	parent crdtID
	tool   uint32
	points []point
}

type page struct {
	lines    []line
	anchorX  map[crdtID]float64 // group node id -> anchor_origin_x
	textTopY float64            // root text block pos_y (0 if absent)
}

type reader struct {
	b   []byte
	pos int
	end int // end of the current block or sub-block
}

var errShort = errors.New("unexpected end of data")

func (r *reader) need(n int) error {
	if r.pos+n > r.end {
		return errShort
	}
	return nil
}

func (r *reader) u8() (uint8, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *reader) u16() (uint16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint16(r.b[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *reader) f32() (float64, error) {
	v, err := r.u32()
	return float64(math.Float32frombits(v)), err
}

func (r *reader) f64() (float64, error) {
	if err := r.need(8); err != nil {
		return 0, err
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(r.b[r.pos:]))
	r.pos += 8
	return v, nil
}

func (r *reader) varuint() (uint64, error) {
	var result uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := r.u8()
		if err != nil {
			return 0, err
		}
		result |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return result, nil
		}
	}
	return 0, errors.New("varuint too long")
}

// peekTag reports whether the next tag is (index, tagType) without consuming it.
func (r *reader) peekTag(index uint64, tagType uint64) bool {
	save := r.pos
	defer func() { r.pos = save }()
	x, err := r.varuint()
	return err == nil && x>>4 == index && x&0xF == tagType
}

func (r *reader) tag(index uint64, tagType uint64) error {
	at := r.pos
	x, err := r.varuint()
	if err != nil {
		return err
	}
	if x>>4 != index || x&0xF != tagType {
		r.pos = at
		return fmt.Errorf("expected tag %d/%x, got %d/%x at %d", index, tagType, x>>4, x&0xF, at)
	}
	return nil
}

func (r *reader) id(index uint64) (crdtID, error) {
	if err := r.tag(index, tagID); err != nil {
		return crdtID{}, err
	}
	p1, err := r.u8()
	if err != nil {
		return crdtID{}, err
	}
	p2, err := r.varuint()
	return crdtID{p1, p2}, err
}

func (r *reader) taggedU32(index uint64) (uint32, error) {
	if err := r.tag(index, tagByte4); err != nil {
		return 0, err
	}
	return r.u32()
}

func (r *reader) taggedF32(index uint64) (float64, error) {
	if err := r.tag(index, tagByte4); err != nil {
		return 0, err
	}
	return r.f32()
}

func (r *reader) taggedF64(index uint64) (float64, error) {
	if err := r.tag(index, tagByte8); err != nil {
		return 0, err
	}
	return r.f64()
}

// subblock reads a sub-block header and returns its end offset.
func (r *reader) subblock(index uint64) (int, error) {
	if err := r.tag(index, tagLength4); err != nil {
		return 0, err
	}
	n, err := r.u32()
	if err != nil {
		return 0, err
	}
	if r.pos+int(n) > r.end {
		return 0, errShort
	}
	return r.pos + int(n), nil
}

// lwwFloat reads a last-write-wins float sub-block: timestamp id(1), float(2).
func (r *reader) lwwFloat(index uint64) (float64, error) {
	end, err := r.subblock(index)
	if err != nil {
		return 0, err
	}
	outer := r.end
	r.end = end
	defer func() { r.pos, r.end = end, outer }()
	if _, err := r.id(1); err != nil {
		return 0, err
	}
	return r.taggedF32(2)
}

func (r *reader) skipSubblock(index uint64) error {
	end, err := r.subblock(index)
	if err == nil {
		r.pos = end
	}
	return err
}

func parsePage(data []byte) (*page, error) {
	if !bytes.HasPrefix(data, []byte(headerV6)) {
		return nil, errors.New("not a v6 page")
	}
	p := &page{anchorX: map[crdtID]float64{}}
	pos := len(headerV6)
	for pos+8 <= len(data) {
		length := int(binary.LittleEndian.Uint32(data[pos:]))
		version := data[pos+6]
		blockType := data[pos+7]
		start := pos + 8
		end := start + length
		if end > len(data) {
			return nil, errShort
		}
		r := &reader{b: data, pos: start, end: end}
		var err error
		switch blockType {
		case blockLineItem:
			err = readLineItem(r, version, p)
		case blockTreeNode:
			err = readTreeNode(r, p)
		case blockRootText:
			err = readRootText(r, p)
		}
		if err != nil {
			return nil, fmt.Errorf("block type %d at %d: %w", blockType, pos, err)
		}
		pos = end
	}
	return p, nil
}

func readLineItem(r *reader, version uint8, p *page) error {
	parent, err := r.id(1)
	if err != nil {
		return err
	}
	item, err := r.id(2)
	if err != nil {
		return err
	}
	if _, err := r.id(3); err != nil { // left
		return err
	}
	if _, err := r.id(4); err != nil { // right
		return err
	}
	if _, err := r.taggedU32(5); err != nil { // deleted length
		return err
	}
	if !r.peekTag(6, tagLength4) {
		return nil // no value: an erased stroke
	}
	valueEnd, err := r.subblock(6)
	if err != nil {
		return err
	}
	r.end = valueEnd
	if _, err := r.u8(); err != nil { // item type (3 = line)
		return err
	}
	tool, err := r.taggedU32(1)
	if err != nil {
		return err
	}
	if _, err := r.taggedU32(2); err != nil { // color
		return err
	}
	if _, err := r.taggedF64(3); err != nil { // thickness scale
		return err
	}
	if _, err := r.taggedF32(4); err != nil { // starting length
		return err
	}
	pointsEnd, err := r.subblock(5)
	if err != nil {
		return err
	}
	size := 14 // v2: x, y float32; speed, width uint16; direction, pressure uint8
	if version == 1 {
		size = 24 // v1: six float32
	}
	n := (pointsEnd - r.pos) / size
	pts := make([]point, 0, n)
	for i := 0; i < n; i++ {
		x, _ := r.f32()
		y, _ := r.f32()
		r.pos += size - 8
		pts = append(pts, point{x, y})
	}
	p.lines = append(p.lines, line{id: item, parent: parent, tool: tool, points: pts})
	return nil
}

func readTreeNode(r *reader, p *page) error {
	node, err := r.id(1)
	if err != nil {
		return err
	}
	if err := r.skipSubblock(2); err != nil { // label
		return err
	}
	if err := r.skipSubblock(3); err != nil { // visible
		return err
	}
	if r.pos >= r.end {
		return nil
	}
	for _, idx := range []uint64{7, 8, 9} { // anchor id, type, threshold
		if err := r.skipSubblock(idx); err != nil {
			return err
		}
	}
	x, err := r.lwwFloat(10) // anchor origin x
	if err != nil {
		return err
	}
	p.anchorX[node] = x
	return nil
}

func readRootText(r *reader, p *page) error {
	if _, err := r.id(1); err != nil {
		return err
	}
	if err := r.skipSubblock(2); err != nil { // text items and formatting
		return err
	}
	end, err := r.subblock(3)
	if err != nil {
		return err
	}
	if _, err := r.f64(); err != nil { // pos x
		return err
	}
	y, err := r.f64()
	if err != nil {
		return err
	}
	r.pos = end
	p.textTopY = y
	return nil
}
