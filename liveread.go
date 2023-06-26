package liveread

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// ERROR_EOF is an alias of `io.EOF`
var ERROR_EOF error = io.EOF

// ERROR_EOF_Save is only returned when using restore points
//
// in most cases, you will need to use `io.EOF` to check for EOF errors
var ERROR_EOF_Save error = errors.New("EOF_Save")


type Reader[T maxLenOpts] struct {
	buf *[]byte
	maxLen uint
	bufSize uint
	intType uint

	overflow *[]byte
	start *T
	ind *T
	size *uint
	eof *bool
	Reader *bufio.Reader
	File *os.File

	writeSleep time.Duration
	readSleep time.Duration

	mu sync.RWMutex
	mu2 sync.RWMutex
	muSave sync.Mutex

	savedBytes *[][]byte
	readSave *[]readRestore
}

type readRestore struct {
	save uint
	ind *uint
	next uint8
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
// Type {uint8|uint16}: this sets the maxLen for how many bytes can be read to the queue before the overflow is used
func Read[T maxLenOpts](path string, bufSize ...uint) (*Reader[T], error) {
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

	writeSleep := 1 * time.Nanosecond
	readSleep := 1 * time.Nanosecond
	if len(bufSize) > 1 {
		writeSleep = time.Duration(bufSize[1]) * time.Nanosecond
	}
	if len(bufSize) > 2 {
		readSleep = time.Duration(bufSize[2]) * time.Nanosecond
	}


	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return &Reader[T]{}, err
	}

	buf := make([]byte, maxLen)
	overflow := []byte{}
	start := T(0)
	ind := T(0)
	size := uint(0)
	eof := false

	reader := bufio.NewReader(file)

	thisReader := Reader[T]{
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

		writeSleep: writeSleep,
		readSleep: readSleep,

		savedBytes: &[][]byte{},
		readSave: &[]readRestore{},
	}

	go func(){
		defer file.Close()

		var b []byte
		var err error
		for err == nil {
			if size >= maxLen {
				time.Sleep(1 * time.Nanosecond)
				continue
			}

			thisReader.mu.Lock()

			if size >= maxLen {
				thisReader.mu.Unlock()
				time.Sleep(writeSleep)
				continue
			}

			s := maxLen - size
			if s > bSize {
				s = bSize
			}

			thisReader.mu2.RLock()

			b, err = reader.Peek(int(s))
			reader.Discard(int(s))

			thisReader.mu2.RUnlock()

			for i := 0; i < len(b); i++ {
				buf[ind] = b[i]
				ind++
			}

			size += uint(len(b))

			thisReader.mu.Unlock()

			time.Sleep(writeSleep)
		}

		eof = true
	}()

	return &thisReader, nil
}

// Get reads bytes from a starting point and grabs a number of bytes equal to the size
//
// if there are no more bytes to read, a smaller size will be returned with an io.EOF error
//
// this method does not discard anything after a read
func (reader *Reader[T]) Get(start uint, size uint) ([]byte, error) {
	// handle experimental restore point if enabled
	if len(*reader.readSave) != 0 {
		reader.muSave.Lock()

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
			if end > ln {
				end -= ln

				if s := end - start; s > size {
					size = s
				}else{
					size = 0
				}

				res = append(res, (*reader.savedBytes)[i][start:ln]...)
			}else{
				reader.muSave.Unlock()
				return (*reader.savedBytes)[i][start:end], nil
			}

			if readSave.next == 0 {
				reader.muSave.Unlock()
				return res, ERROR_EOF_Save
			}else if readSave.next == 2 {
				reader.muSave.Unlock()
				return res, nil
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
					reader.muSave.Unlock()
					return append(res, (*reader.savedBytes)[i][:ln]...), ERROR_EOF_Save
				}else{
					reader.muSave.Unlock()
					return append(res, (*reader.savedBytes)[i][:size]...), nil
				}
			}

			reader.muSave.Unlock()
			return res, nil
		}

		reader.muSave.Unlock()
	}

	end := start + size
	if end > reader.maxLen {
		s := int(end) - int(reader.maxLen)

		reader.mu.RLock()
		for !*reader.eof && *reader.size < reader.maxLen {
			reader.mu.RUnlock()
			time.Sleep(reader.readSleep)
			reader.mu.RLock()
		}

		if s > len(*reader.overflow) {
			reader.mu2.Lock()
			b, _ := reader.Reader.Peek(s - len(*reader.overflow))
			reader.Reader.Discard(s - len(*reader.overflow))
			reader.mu2.Unlock()

			*reader.overflow = append(*reader.overflow, b...)

			*reader.size += uint(len(b))
		}

		b := make([]byte, size)
		bLen := uint(0)

		var e uint
		if start > reader.maxLen {
			e = start-reader.maxLen

			for i := 0; i < s && bLen < size; i++ {
				if i >= len((*reader.overflow)) {
					break
				}
				b[i] = (*reader.overflow)[e+uint(i)]
				bLen++
			}
		}else{
			e = reader.maxLen-start

			j := *reader.start + T(start)
			for i := uint(0); i < e && bLen < size; i++ {
				if (*reader.buf)[j] == 0 {
					break
				}
				b[i] = (*reader.buf)[j]
				j++
				bLen++
			}

			for i := 0; i < s && bLen < size; i++ {
				if i >= len((*reader.overflow)) {
					break
				}
				b[e+uint(i)] = (*reader.overflow)[i]
				bLen++
			}
		}

		reader.mu.RUnlock()

		if bLen < size {
			return b[:bLen], io.EOF
		}
		return b[:bLen], nil
	}

	reader.mu.RLock()
	for !*reader.eof && *reader.size < end {
		reader.mu.RUnlock()
		time.Sleep(reader.readSleep)
		reader.mu.RLock()
	}

	b := make([]byte, size)
	j := *reader.start + T(start)
	bLen := uint(0)
	for i := uint(0); i < size; i++ {
		if (*reader.buf)[j] == 0 {
			break
		}
		b[i] = (*reader.buf)[j]
		j++
		bLen++
	}

	reader.mu.RUnlock()

	if bLen < size {
		return b[:bLen], io.EOF
	}
	return b[:bLen], nil
}

// getLocal is a clone of Get that does not run muSave.lock()
//
// note: this method will still run the regular mu.Lock() method
func (reader *Reader[T]) getLocal(start uint, size uint) ([]byte, error) {
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
			if end > ln {
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
	
			if readSave.next == 0 {
				return res, ERROR_EOF_Save
			}else if readSave.next == 2 {
				return res, nil
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
		s := int(end) - int(reader.maxLen)

		reader.mu.RLock()
		for !*reader.eof && *reader.size < reader.maxLen {
			reader.mu.RUnlock()
			time.Sleep(reader.readSleep)
			reader.mu.RLock()
		}

		if s > len(*reader.overflow) {
			reader.mu2.Lock()
			b, _ := reader.Reader.Peek(s - len(*reader.overflow))
			reader.Reader.Discard(s - len(*reader.overflow))
			reader.mu2.Unlock()

			*reader.overflow = append(*reader.overflow, b...)

			*reader.size += uint(len(b))
		}

		e := reader.maxLen-start

		b := make([]byte, size)
		j := *reader.start + T(start)
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

		reader.mu.RUnlock()

		if bLen < size {
			return b[:bLen], io.EOF
		}
		return b[:bLen], nil
	}

	reader.mu.RLock()
	for !*reader.eof && *reader.size < end {
		reader.mu.RUnlock()
		time.Sleep(reader.readSleep)
		reader.mu.RLock()
	}

	b := make([]byte, size)
	j := *reader.start + T(start)
	bLen := uint(0)
	for i := uint(0); i < size; i++ {
		if (*reader.buf)[j] == 0 {
			break
		}
		b[i] = (*reader.buf)[j]
		j++
		bLen++
	}

	reader.mu.RUnlock()

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
func (reader *Reader[T]) Peek(size uint) ([]byte, error) {
	return reader.Get(0, size)
}

// PeekByte returns a single byte at an index
//
// if there are no more bytes to read, a smaller size will be returned with an io.EOF error
//
// this mothod is similar the Peek method, it does not discard anything after a read
func (reader *Reader[T]) PeekByte(index uint) (byte, error) {
	b, err := reader.Get(index, 1)
	if len(b) == 0 {
		return 0, err
	}
	return b[0], err
}

// PeekBytes returns bytes until the first occurrence of delim
//
// this mothod is similar the Peek method, it does not discard anything after a read
func (reader *Reader[T]) PeekBytes(start uint, delim byte) ([]byte, error) {
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
func (reader *Reader[T]) PeekToBytes(start uint, delim []byte) ([]byte, error) {
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
func (reader *Reader[T]) Discard(size uint) (discarded uint, err error) {
	// handle experimental restore point if enabled
	if l := uint(len(*reader.savedBytes)); l != 0 {
		reader.muSave.Lock()

		if len(*reader.readSave) != 0 {
			saveInd := (*reader.readSave)[len(*reader.readSave)-1].save
			si := l - saveInd
			if si != 0 {
				b, _ := reader.getLocal(0, size)
				(*reader.savedBytes)[si-1] = append((*reader.savedBytes)[si-1], b...)
			}

			*(*reader.readSave)[len(*reader.readSave)-1].ind += size
			reader.muSave.Unlock()
			return
		}

		b, _ := reader.Peek(size)
		(*reader.savedBytes)[l-1] = append((*reader.savedBytes)[l-1], b...)

		reader.muSave.Unlock()
	}

	reader.mu.Lock()

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

		if s < uint(len(*reader.overflow)) {
			*reader.overflow = (*reader.overflow)[s:]
		}else{
			reader.mu2.Lock()
			_, err = reader.Reader.Discard(int(s) - len(*reader.overflow))
			reader.mu2.Unlock()
			*reader.overflow = []byte{}
		}

		*reader.size -= bLen
		*reader.start = j

		reader.mergeOverflow()

		reader.mu.Unlock()

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

	if bLen < size {
		reader.mu2.Lock()
		_, err = reader.Reader.Discard(int(bLen - size))
		reader.mu2.Unlock()
	}

	s := size - bLen
	if s >= uint(len(*reader.overflow)) {
		reader.mu2.Lock()
		_, err = reader.Reader.Discard(int(s) - len(*reader.overflow))
		reader.mu2.Unlock()
		*reader.overflow = []byte{}
	}

	reader.mergeOverflow()

	reader.mu.Unlock()

	if err != nil || bLen < size {
		return bLen, io.EOF
	}
	return bLen, nil
}

// mergeOverflow is a core method used by the Discard method to move the overflow into the buffer
//
// this method will avoid moving more than it should, and will keep any leftover overflow if necessary
func (reader *Reader[T]) mergeOverflow() {
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
func (reader *Reader[T]) ReadByte() (byte, error) {
	b, err := reader.Get(0, 1)
	reader.Discard(1)
	if len(b) == 0 {
		return 0, err
	}
	return b[0], err
}

// ReadBytes returns bytes until the first occurrence of delim
func (reader *Reader[T]) ReadBytes(delim byte) ([]byte, error) {
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
func (reader *Reader[T]) ReadToBytes(delim []byte) ([]byte, error) {
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
func (reader *Reader[T]) Buffered() int {
	reader.mu2.RLock()
	s := reader.Reader.Buffered()
	reader.mu2.RUnlock()
	return s
}

// Size returns the size of the underlying buffer in bytes
func (reader *Reader[T]) Size() int {
	reader.mu2.RLock()
	s := reader.Reader.Size()
	reader.mu2.RUnlock()
	return s
}

// Seek sets the offset for the next Read or Write on file to offset, interpreted
// according to whence: 0 means relative to the origin of the file, 1 means
// relative to the current offset, and 2 means relative to the end.
// It returns the new offset and an error, if any.
// The behavior of Seek on a file opened with O_APPEND is not specified.
func (reader *Reader[T]) Seek(offset int64, whence int) (ret int64, err error) {
	reader.mu2.Lock()
	ret, err = reader.File.Seek(offset, whence)
	reader.Reader.Reset(reader.File)
	reader.mu2.Unlock()
	return ret, err
}


// experimental functions

// Experimental:
// Save creates a new restore point to bring back the next set of discarded bytes
//
// note: not all reading methods will support save points, and some may continue to use the file reader
func (reader *Reader[T]) Save() {
	reader.muSave.Lock()

	*reader.savedBytes = append(*reader.savedBytes, []byte{})

	reader.muSave.Unlock()
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
// if @offset[1] == 2, then it will act like `0`, but will return nil in place of an error
//
// by default (@offset[1] == 0), if the restore point runs out of bytes, it will return the error `liveread.ERROR_EOF_Save`
//
// note: this method will append a restore reader to a list, and running the UnRestore method will revert back to the previous restore reader if one was active
func (reader *Reader[T]) Restore(offset ...uint) {
	reader.muSave.Lock()

	if len(*reader.savedBytes) == 0 {
		reader.muSave.Unlock()
		return
	}

	i := uint(1)
	if len(offset) != 0 {
		i = offset[0]
	}

	next := uint8(0)
	if len(offset) > 1 {
		next = uint8(offset[1])
	}

	ind := uint(0)
	*reader.readSave = append(*reader.readSave, readRestore{
		save: i,
		ind: &ind,
		next: next,
	})

	reader.muSave.Unlock()
}

// Experimental:
// RestoreReset will reset the last restore point without creating a new one
func (reader *Reader[T]) RestoreReset(offset ...uint) {
	reader.muSave.Lock()

	l := uint(len(*reader.readSave))

	if l == 0 {
		reader.muSave.Unlock()
		return
	}

	i := uint(1)
	if len(offset) != 0 {
		i = offset[0]
	}

	next := uint8(0)
	if len(offset) > 1 {
		next = uint8(offset[1])
	}

	*(*reader.readSave)[l-1].ind -= *(*reader.readSave)[l-1].ind
	(*reader.readSave)[l-1].next = next
	(*reader.readSave)[l-1].save = i

	reader.muSave.Unlock()
}

// Experimental:
// RestoreReset will reset the first restore point without creating a new one
func (reader *Reader[T]) RestoreResetFirst(offset ...uint) {
	reader.muSave.Lock()

	if len(*reader.readSave) == 0 {
		reader.muSave.Unlock()
		return
	}

	i := uint(1)
	if len(offset) != 0 {
		i = offset[0]
	}

	next := uint8(0)
	if len(offset) > 1 {
		next = uint8(offset[1])
	}

	*(*reader.readSave)[0].ind -= *(*reader.readSave)[0].ind
	(*reader.readSave)[0].next = next
	(*reader.readSave)[0].save = i

	reader.muSave.Unlock()
}

// Experimental:
// UnRestore removes a restore reader from the end of the list
func (reader *Reader[T]) UnRestore() {
	reader.muSave.Lock()

	if len(*reader.readSave) != 0 {
		*reader.readSave = (*reader.readSave)[:len(*reader.readSave)-1]
	}

	reader.muSave.Unlock()
}

// Experimental:
// UnRestoreFirst removes a restore reader from the start of list
func (reader *Reader[T]) UnRestoreFirst() {
	reader.muSave.Lock()

	if len(*reader.readSave) != 0 {
		*reader.readSave = (*reader.readSave)[1:]
	}

	reader.muSave.Unlock()
}

// Experimental:
// DelSave removes the last restore point and may shift the one being used
func (reader *Reader[T]) DelSave() {
	reader.muSave.Lock()

	if len(*reader.savedBytes) != 0 {
		*reader.savedBytes = (*reader.savedBytes)[:len(*reader.savedBytes)-1]
	}

	if len(*reader.readSave) > len(*reader.savedBytes) {
		*reader.readSave = (*reader.readSave)[:len(*reader.readSave)-1]
	}

	reader.muSave.Unlock()
}

// Experimental:
// DelFirstSave removes the first restore point and may shift the one being used
func (reader *Reader[T]) DelFirstSave() {
	reader.muSave.Lock()

	if len(*reader.savedBytes) != 0 {
		*reader.savedBytes = (*reader.savedBytes)[1:]
	}

	if len(*reader.readSave) > len(*reader.savedBytes) {
		*reader.readSave = (*reader.readSave)[1:]
	}

	reader.muSave.Unlock()
}
