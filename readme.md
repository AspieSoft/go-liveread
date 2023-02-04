# Go Live Read

[![donation link](https://img.shields.io/badge/buy%20me%20a%20coffee-paypal-blue)](https://paypal.me/shaynejrtaylor?country.x=US&locale.x=en_US)

A module that enables you to concurrently read a file and do something with the data.

## Installation

```shell script

  go get github.com/AspieSoft/go-liveread

```

## Usage

```go

import (
  "github.com/AspieSoft/go-liveread"
)

func main(){
  reader, err := liveread.Read("my/file.txt")
  
  // read/peek at the first 4 bytes
  b, err := reader.Get(0, 4)

  // read/peek at 4 bytes starting at index 12
  b, err := reader.Get(12, 4)

  // peek at the next 10 bytes
  b, err := reader.Peek(10)

  // discard the bytes that you no longer need to read
  discarded, err := reader.Discard(10)

  // read until you reach a specific byte
  b, err := reader.ReadBytes('\n')

  // similar to ReadBytes, but does not discard automatically
  // also allows you to specify a starting point
  b, err := reader.PeekBytes(0, '\n')

  // read to a byte array instead of just a single byte
  b, err := reader.ReadToBytes([]byte("/>"))
}

```
