package tindex

import (
	"encoding/binary"
	"errors"
	"io"
)

const pageSize = 2048

var errPageFull = errors.New("page full")

type pageCursor interface {
	iterator
	append(v uint64) error
}

type page interface {
	cursor() pageCursor
	init(v uint64) error
	data() []byte
}

type pageDelta struct {
	b []byte
}

func newPageDelta(data []byte) *pageDelta {
	return &pageDelta{b: data}
}

func (p *pageDelta) init(v uint64) error {
	// Write first value.
	binary.PutUvarint(p.b, v)
	return nil
}

func (p *pageDelta) cursor() pageCursor {
	return &pageDeltaCursor{data: p.b}
}

func (p *pageDelta) data() []byte {
	return p.b
}

type pageDeltaCursor struct {
	data []byte
	pos  int
	cur  uint64
}

func (p *pageDeltaCursor) append(id uint64) error {
	// Run to the end.
	_, err := p.next()
	for ; err == nil; _, err = p.next() {
		// Consume.
	}
	if err != io.EOF {
		return err
	}
	if len(p.data)-p.pos < binary.MaxVarintLen64 {
		return errPageFull
	}
	if p.cur >= id {
		return errOutOfOrder
	}
	p.pos += binary.PutUvarint(p.data[p.pos:], id-p.cur)
	p.cur = id

	return nil
}

func (p *pageDeltaCursor) seek(min uint64) (v uint64, err error) {
	if min < p.cur {
		p.pos = 0
	} else if min == p.cur {
		return p.cur, nil
	}
	for v, err = p.next(); err == nil && v < min; v, err = p.next() {
		// Consume.
	}
	return p.cur, err
}

func (p *pageDeltaCursor) next() (uint64, error) {
	var n int
	if p.pos == 0 {
		p.cur, n = binary.Uvarint(p.data)
	} else {
		var dv uint64
		dv, n = binary.Uvarint(p.data[p.pos:])
		if n <= 0 || dv == 0 {
			return 0, io.EOF
		}
		p.cur += dv
	}
	p.pos += n

	return p.cur, nil
}