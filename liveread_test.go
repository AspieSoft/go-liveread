package liveread

import (
	"bytes"
	"testing"
	"time"
)

func Test(t *testing.T){
	reader, err := Read[uint8]("test.txt")
	if err != nil {
		t.Error(err)
		return
	}

	b, err := reader.Get(0, 4)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(b, []byte("this")) {
		t.Error("1st Get Method Provided The Wrong Output:", string(b))
		return
	}

	_, err = reader.Discard(5)
	if err != nil {
		t.Error(err)
		return
	}

	b, err = reader.Get(0, 4)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(b, []byte("is a")) {
		t.Error("2nd Get Method Provided The Wrong Output:", string(b))
		return
	}

	_, err = reader.Discard(5)
	if err != nil {
		t.Error(err)
		return
	}

	b, err = reader.Get(0, 4)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(b, []byte("test")) {
		t.Error("3rd Get Method Provided The Wrong Output:", string(b))
		return
	}

	time.Sleep(1 * time.Second)
}
