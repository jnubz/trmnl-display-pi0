package main

import (
    "fmt"
    waveshare "github.com/wiless/waveshare"
)

func main() {
    display, _ := waveshare.NewEPD(waveshare.EPD7in5v2, 17, 25, 8, 24)
    display.Init()
    buffer := make([]byte, 800*480/8)
    for i := 0; i < len(buffer)/2; i++ {
        buffer[i] = 0x00 // Half black
    }
    for i := len(buffer)/2; i < len(buffer); i++ {
        buffer[i] = 0xFF // Half white
    }
    display.Display(buffer)
    fmt.Println("Test image displayed")
    display.Sleep()
}