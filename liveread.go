package liveread

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"time"
)

// max length for uint8
const readMaxLen uint = 256

// max buffer size to read at a time
const readBufSize uint = 10

type Reader struct {
	// buf *map[uint8]byte
	buf *[]byte
	overflow *[]byte
	start *uint8
	ind *uint8
	size *uint
	eof *bool
	Reader *bufio.Reader
	File *os.File

	reading *bool
	handlingOverflow *uint
	handlingDiscard *uint
}

func Read(path string) (*Reader, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return &Reader{}, err
	}

	buf := make([]byte, readMaxLen)
	overflow := []byte{}
	start := uint8(0)
	ind := uint8(0)
	size := uint(0)
	eof := false

	reading := false
	handlingOverflow := uint(0)
	handlingDiscard := uint(0)

	reader := bufio.NewReader(file)

	go func(){
		defer file.Close()

		var b []byte
		var err error
		for err == nil {
			if handlingDiscard > 0 {
				time.Sleep(10 * time.Nanosecond)
				continue
			}

			if size >= readMaxLen {
				time.Sleep(1 * time.Nanosecond)
				continue
			}

			reading = true

			s := readMaxLen - size
			if s > readBufSize {
				s = readBufSize
			}

			b, err = reader.Peek(int(s))
			reader.Discard(int(s))

			for i := 0; i < len(b); i++ {
				buf[ind] = b[i]
				ind++
			}

			time.Sleep(1 * time.Nanosecond)
			size += uint(len(b))

			reading = false
		}

		eof = true
	}()

	return &Reader{
		buf: &buf,
		overflow: &overflow,
		start: &start,
		ind: &ind,
		size: &size,
		eof: &eof,
		Reader: reader,
		File: file,

		reading: &reading,
		handlingOverflow: &handlingOverflow,
		handlingDiscard: &handlingDiscard,
	}, nil
}

// Get reads bytes from a starting point and grabs a number of bytes equal to the size
//
// if there are no more bytes to read, a smaller size will be returned with an io.EOF error
//
// this method does not discard anything after a read
func (reader *Reader) Get(start uint, size uint) ([]byte, error) {
	for *reader.handlingDiscard > 0 {
		time.Sleep(10 * time.Nanosecond)
	}

	end := start + size
	if end > readMaxLen {
		for !*reader.eof && *reader.size < readMaxLen {
			time.Sleep(1 * time.Nanosecond)
		}

		s := int(end) - int(readMaxLen)

		if s > len(*reader.overflow) {
			for *reader.handlingOverflow > 0 {
				time.Sleep(10 * time.Nanosecond)
			}

			if s > len(*reader.overflow) {
				*reader.handlingOverflow++
				time.Sleep(10 * time.Nanosecond)
				if *reader.handlingOverflow > 1 {
					*reader.handlingOverflow--
					return reader.Get(start, size)
				}

				b, _ := reader.Reader.Peek(s - len(*reader.overflow))
				reader.Reader.Discard(s - len(*reader.overflow))

				*reader.overflow = append(*reader.overflow, b...)

				*reader.size += uint(len(b))

				*reader.handlingOverflow--
			}
		}

		e := readMaxLen-start

		b := make([]byte, size)
		j := *reader.start + uint8(start)
		bLen := uint(0)
		for i := uint(0); i < e; i++ {
			if (*reader.buf)[j] == 0 {
				break
			}
			b[i] = (*reader.buf)[j]
			j++
			bLen++
		}

		for i := 0; i < s; i++ {
			if i >= len((*reader.overflow)) {
				break
			}
			b[e+uint(i)] = (*reader.overflow)[i]
			bLen++
		}

		if bLen < size {
			return b[:bLen], io.EOF
		}
		return b[:bLen], nil
	}

	for !*reader.eof && *reader.size < end {
		time.Sleep(1 * time.Nanosecond)
	}

	b := make([]byte, size)
	j := *reader.start + uint8(start)
	bLen := uint(0)
	for i := uint(0); i < size; i++ {
		if (*reader.buf)[j] == 0 {
			break
		}
		b[i] = (*reader.buf)[j]
		j++
		bLen++
	}

	if bLen < size {
		return b[:bLen], io.EOF
	}
	return b[:bLen], nil
}

// Peek reads a number of bytes equal to the size
//
// if there are no more bytes to read, a smaller size will be returned with an io.EOF error
//
// this method does not discard anything after a read
func (reader *Reader) Peek(size uint) ([]byte, error) {
	return reader.Get(0, size)
}

// PeekByte returns a single byte at an index
//
// if there are no more bytes to read, a smaller size will be returned with an io.EOF error
//
// this mothod is similar the Peek method, it does not discard anything after a read
func (reader *Reader) PeekByte(index uint) (byte, error) {
	b, err := reader.Get(index, 1)
	if len(b) == 0 {
		return 0, err
	}
	return b[0], err
}

// PeekBytes returns bytes until the first occurrence of delim
//
// this mothod is similar the Peek method, it does not discard anything after a read
func (reader *Reader) PeekBytes(start uint, delim byte) ([]byte, error) {
	res := []byte{}
	
	var b byte
	var err error
	for err == nil && b != delim {
		b, err = reader.PeekByte(start)
		if b != 0 {
			res = append(res, b)
		}
		start++
	}

	return res, err
}

// PeekToBytes is similar to PeekBytes, but it allows you to detect a []byte instead
func (reader *Reader) PeekToBytes(start uint, delim []byte) ([]byte, error) {
	res := []byte{}
	
	var b byte
	var err error
	for err == nil && !bytes.HasSuffix(res, delim) {
		b, err = reader.PeekByte(start)
		if b != 0 {
			res = append(res, b)
		}
		start++
	}

	return res, err
}

// Discard removes a number of bytes from memory when they no longer are needed
//
// if there are no more bytes to remove, a smaller size will be returned with an io.EOF error
func (reader *Reader) Discard(size uint) (discarded uint, err error) {
	*reader.handlingDiscard++

	for *reader.handlingOverflow > 0 {
		time.Sleep(10 * time.Nanosecond)
	}

	*reader.handlingOverflow++
	time.Sleep(10 * time.Nanosecond)
	if *reader.handlingOverflow > 1 {
		*reader.handlingOverflow--
		*reader.handlingDiscard--
		return reader.Discard(size)
	}

	for *reader.reading {
		time.Sleep(10 * time.Nanosecond)
	}

	if size > readMaxLen {
		j := *reader.start
		bLen := uint(0)
		for i := uint(0); i < readMaxLen; i++ {
			if (*reader.buf)[j] == 0 {
				break
			}
			(*reader.buf)[j] = 0
			j++
			bLen++
		}

		s := size - bLen
		for i := uint(0); i < s; i++ {
			*reader.ind++
			j++
		}
		bLen += s

		// var err error
		if s < uint(len(*reader.overflow)) {
			*reader.overflow = (*reader.overflow)[s:]
		}else{
			_, err = reader.Reader.Discard(int(s) - len(*reader.overflow))
			*reader.overflow = []byte{}
		}

		*reader.size -= bLen
		*reader.start = j

		reader.mergeOverflow()

		*reader.handlingOverflow--
		*reader.handlingDiscard--

		if err != nil || bLen < size {
			return bLen, io.EOF
		}
		return bLen, nil
	}

	j := *reader.start
	bLen := uint(0)
	for i := uint(0); i < size; i++ {
		if (*reader.buf)[j] == 0 {
			break
		}
		(*reader.buf)[j] = 0
		j++
		bLen++
	}

	*reader.start = j
	*reader.size -= bLen

	// var err error
	if bLen < size {
		_, err = reader.Reader.Discard(int(bLen - size))
	}

	s := size - bLen
	if s >= uint(len(*reader.overflow)) {
		_, err = reader.Reader.Discard(int(s) - len(*reader.overflow))
		*reader.overflow = []byte{}
	}

	reader.mergeOverflow()

	*reader.handlingOverflow--
	*reader.handlingDiscard--

	if err != nil || bLen < size {
		return bLen, io.EOF
	}
	return bLen, nil
}

// mergeOverflow is a core method used by the Discard method to move the overflow into the buffer
//
// this method will avoid moving more than it should, and will keep any leftover overflow if necessary
func (reader *Reader) mergeOverflow() {
	bLen := uint(0)
	for i := 0; i < len(*reader.overflow); i++ {
		if bLen >= readMaxLen || (*reader.buf)[*reader.ind] != 0 {
			break
		}

		(*reader.buf)[*reader.ind] = (*reader.overflow)[i]
		*reader.ind++
		bLen++
	}

	if bLen < uint(len(*reader.overflow)) {
		*reader.overflow = (*reader.overflow)[bLen:]
	}else{
		*reader.overflow = []byte{}
	}

	*reader.size += bLen
}


// ReadByte returns a single byte at an index
func (reader *Reader) ReadByte() (byte, error) {
	b, err := reader.Get(0, 1)
	reader.Discard(1)
	if len(b) == 0 {
		return 0, err
	}
	return b[0], err
}

// ReadBytes returns bytes until the first occurrence of delim
func (reader *Reader) ReadBytes(delim byte) ([]byte, error) {
	res := []byte{}
	
	var b byte
	var err error
	for err == nil && b != delim {
		b, err = reader.ReadByte()
		if b != 0 {
			res = append(res, b)
		}
	}

	return res, err
}

// ReadToBytes is similar to ReadBytes, but it allows you to detect a []byte instead
func (reader *Reader) ReadToBytes(delim []byte) ([]byte, error) {
	res := []byte{}
	
	var b byte
	var err error
	for err == nil && !bytes.HasSuffix(res, delim) {
		b, err = reader.ReadByte()
		if b != 0 {
			res = append(res, b)
		}
	}

	return res, err
}


// other functions

// Buffered returns the number of bytes that can be read from the current buffer
func (reader *Reader) Buffered() int {
	return reader.Reader.Buffered()
}

// Size returns the size of the underlying buffer in bytes
func (reader *Reader) Size() int {
	return reader.Reader.Size()
}

// Seek sets the offset for the next Read or Write on file to offset, interpreted
// according to whence: 0 means relative to the origin of the file, 1 means
// relative to the current offset, and 2 means relative to the end.
// It returns the new offset and an error, if any.
// The behavior of Seek on a file opened with O_APPEND is not specified.
func (reader *Reader) Seek(offset int64, whence int) (ret int64, err error) {
	ret, err = reader.File.Seek(offset, whence)
	reader.Reader.Reset(reader.File)

	return ret, err
}
