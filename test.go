package main

import (
    "fmt"
    "image"
    "image/color"
    waveshare "github.com/ChristianHering/WaveShare"
)

func main() {
    waveshare.Initialize()
    fmt.Println("Initialized display")

    // Create a half-black, half-white image
    img := image.NewGray(image.Rect(0, 0, 800, 480))
    for y := 0; y < 480; y++ {
        for x := 0; x < 800; x++ {
            if x < 400 {
                img.SetGray(x, y, color.Gray{0})   // Black
            } else {
                img.SetGray(x, y, color.Gray{255}) // White
            }
        }
    }

    waveshare.DisplayImage(img)
    fmt.Println("Test image displayed")

    waveshare.Sleep()
    waveshare.Exit()
}