package liveread

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"time"
)

// ERROR_EOF is an alias of `io.EOF`
var ERROR_EOF error = io.EOF

// ERROR_EOF_Save is only returned when using restore points
//
// in most cases, you will need to use `io.EOF` to check for EOF errors
var ERROR_EOF_Save error = errors.New("EOF_Save")


type Reader struct {
	// buf *map[uint8]byte
	buf *[]byte
	maxLen uint
	bufSize uint

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

	savedBytes *[][]byte
	readSave *[]readRestore
}

type readRestore struct {
	save uint
	ind *uint
	next bool
}

type maxLenOpts interface{uint8|uint16}
func getMaxLen[T maxLenOpts]() uint {
	var t interface{} = uint8(0)
	if _, ok := t.(T); ok {
		return 256
	}

	t = uint16(0)
	if _, ok := t.(T); ok {
		return 65536
	}

	return 256
}

// Read a file and concurrently peek at the bytes before it is done being read
//
// @path: the file path you wish to read
//
// @bufSize: the number of bytes to read at a time (min = 10, max = maxLen/4)
//
// Type {uint8|uint16|uint32}: this sets the maxLen for how many bytes can be read to the queue before the overflow is used
func Read[T maxLenOpts](path string, bufSize ...uint) (*Reader, error) {
	var maxLen uint = getMaxLen[T]()

	var bSize uint = 10
	if len(bufSize) != 0 {
		bSize = bufSize[0]
		if bSize < 10 {
			bSize = 10
		}else if bSize > maxLen/4 {
			bSize = maxLen/4
		}
	}


	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return &Reader{}, err
	}

	buf := make([]byte, maxLen)
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

			if size >= maxLen {
				time.Sleep(1 * time.Nanosecond)
				continue
			}

			reading = true

			s := maxLen - size
			if s > bSize {
				s = bSize
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

	readSave := []readRestore{}

	return &Reader{
		buf: &buf,
		maxLen: maxLen,
		bufSize: bSize,

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

		savedBytes: &[][]byte{},
		readSave: &readSave,
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

	// handle experimental restore point if enabled
	if len(*reader.readSave) != 0 {
		if readSave := (*reader.readSave)[len(*reader.readSave)-1]; readSave.save != 0 {
			l := uint(len(*reader.savedBytes))
			i := readSave.save
			if l > i {
				i = l - i
			}else{
				i = 0
			}
	
			start += *readSave.ind
	
			res := []byte{}
	
			ln := uint(len((*reader.savedBytes)[i]))
			end := start + size
			if end >= ln {
				end -= ln
	
				if s := end - start; s > size {
					size = s
				}else{
					size = 0
				}
	
				res = append(res, (*reader.savedBytes)[i][start:ln]...)
			}else{
				return (*reader.savedBytes)[i][start:end], nil
			}
	
			if !readSave.next {
				return res, ERROR_EOF_Save
			}
	
			for ; size != 0 && i != 0; i-- {
				ln = uint(len((*reader.savedBytes)[i]))
				if size >= ln {
					size -= ln
					res = append(res, (*reader.savedBytes)[i][:ln]...)
				}else{
					res = append(res, (*reader.savedBytes)[i][:size]...)
					size = 0
					break
				}
			}
	
			if size != 0 {
				ln = uint(len((*reader.savedBytes)[i]))
				if size >= ln {
					return append(res, (*reader.savedBytes)[i][:ln]...), ERROR_EOF_Save
				}else{
					return append(res, (*reader.savedBytes)[i][:size]...), nil
				}
			}
	
			return res, nil
		}
	}

	end := start + size
	if end > reader.maxLen {
		for !*reader.eof && *reader.size < reader.maxLen {
			time.Sleep(1 * time.Nanosecond)
		}

		s := int(end) - int(reader.maxLen)

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

		e := reader.maxLen-start

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
	if len(*reader.savedBytes) != 0 {
		b, _ := reader.Peek(size)
		(*reader.savedBytes)[len(*reader.savedBytes)-1] = append((*reader.savedBytes)[len(*reader.savedBytes)-1], b...)

		if len(*reader.readSave) != 0 {
			*(*reader.readSave)[len(*reader.readSave)-1].ind += size
			return
		}
	}
	
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

	if size > reader.maxLen {
		j := *reader.start
		bLen := uint(0)
		for i := uint(0); i < reader.maxLen; i++ {
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
		if bLen >= reader.maxLen || (*reader.buf)[*reader.ind] != 0 {
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


// experimental functions


// Experimental:
// Save creates a new restore point to bring back the next set of discarded bytes
//
// note: not all reading methods will support save points, and some may continue to use the file reader
func (reader *Reader) Save() {
	*reader.savedBytes = append(*reader.savedBytes, []byte{})
}

// Experimental:
// Restore tells the reading functions to start pulling from the restore point chosen
//
// @offset[0] says which save index to pill from, starting with the last index at 1
//
// if @offset[0] == 0, the save index will be turned off, and the restore point will continue to pull from the original buffer
//
// note: this method will append a restore reader to a list, and running the UnRestore method will revert back to the previous restore reader if one was active
func (reader *Reader) Restore(offset ...uint) {
	i := uint(1)
	if len(offset) != 0 {
		i = offset[0]
	}

	next := false
	if len(offset) > 1 && offset[1] != 0 {
		next = true
	}

	ind := uint(0)
	*reader.readSave = append(*reader.readSave, readRestore{
		save: i,
		ind: &ind,
		next: next,
	})
}

// Experimental:
// Restore tells the reading functions to start pulling from the restore point chosen
//
// @offset[0] says which save index to pill from, starting with the last index at 1
//
// if @offset[0] == 0, the save index will be turned off, and the restore point will continue to pull from the original buffer
//
// if @offset[1] == 1, then when the restore point runs out of bytes, the previous restore point, or main reader, will start to get used
//
// by default (@offset[1] == 0), if the restore point runs out of bytes, it will return the error `liveread.ERROR_EOF_Save`
//
// note: this method will append a restore reader to a list, and running the UnRestore method will revert back to the previous restore reader if one was active
func (reader *Reader) RestoreReset(offset ...uint) {
	i := uint(1)
	if len(offset) != 0 {
		i = offset[0]
	}

	next := false
	if len(offset) > 1 && offset[1] != 0 {
		next = true
	}

	ind := uint(0)
	*reader.readSave = append(*reader.readSave, readRestore{
		save: i,
		ind: &ind,
		next: next,
	})
}

// Experimental:
// UnRestore removes a restore reader from the end of the list
func (reader *Reader) UnRestore() {
	if len(*reader.readSave) != 0 {
		*reader.readSave = (*reader.readSave)[:len(*reader.readSave)-1]
	}
}

// Experimental:
// UnRestoreFirst removes a restore reader from the start of list
func (reader *Reader) UnRestoreFirst() {
	if len(*reader.readSave) != 0 {
		*reader.readSave = (*reader.readSave)[1:]
	}
}

// Experimental:
// DelSave removes the lase restore point and may shift the one being used
func (reader *Reader) DelSave() {
	if len(*reader.savedBytes) != 0 {
		*reader.savedBytes = (*reader.savedBytes)[:len(*reader.savedBytes)-1]
	}
}

// Experimental:
// DelFirstSave removes the first restore point and may shift the one being used
func (reader *Reader) DelFirstSave() {
	if len(*reader.savedBytes) != 0 {
		*reader.savedBytes = (*reader.savedBytes)[1:]
	}
}
