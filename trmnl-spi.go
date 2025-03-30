package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/disintegration/imaging"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/physic"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/host/v3"
	_ "image/jpeg"
	_ "image/png"
)

// Version information
var (
	version   = "0.1.0"
	commit    = "unknown"
	buildDate = "unknown"
)

// TerminalResponse represents the JSON structure returned by the API
type TerminalResponse struct {
	ImageURL    string `json:"image_url"`
	Filename    string `json:"filename"`
	RefreshRate int    `json:"refresh_rate"`
}

// Config holds application configuration
type Config struct {
	APIKey string
}

// AppOptions holds command line options
type AppOptions struct {
	DarkMode bool
	Verbose  bool
}

// EPD holds the display configuration
type EPD struct {
	rstPin  gpio.PinIO
	dcPin   gpio.PinIO
	csPin   gpio.PinIO
	busyPin gpio.PinIO
	pwrPin  gpio.PinIO
	spiPort spi.PortCloser
	conn    spi.Conn
	Width   int
	Height  int
}

var (
	epd *EPD
)

func main() {
	options := parseCommandLineArgs()

	err := initDisplay()
	if err != nil {
		fmt.Printf("Error initializing e-ink display: %v\n", err)
		os.Exit(1)
	}
	defer cleanupDisplay()

	configDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory: %v\n", err)
		os.Exit(1)
	}
	configDir = filepath.Join(configDir, ".trmnl")
	err = os.MkdirAll(configDir, 0755)
	if err != nil {
		fmt.Printf("Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	config := loadConfig(configDir)
	if config.APIKey == "" {
		config.APIKey = os.Getenv("TRMNL_API_KEY")
	}
	if config.APIKey == "" {
		fmt.Println("TRMNL API Key not found.")
		fmt.Print("Please enter your TRMNL API Key: ")
		fmt.Scanln(&config.APIKey)
		saveConfig(configDir, config)
	}

	tmpDir, err := os.MkdirTemp("", "trmnl-display")
	if err != nil {
		fmt.Printf("Error creating temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	clearDisplay()
	testDisplay()

	for {
		processNextImage(tmpDir, config.APIKey, options)
	}
}

func initDisplay() error {
	if _, err := host.Init(); err != nil {
		return fmt.Errorf("error initializing periph: %v", err)
	}

	rstPin := gpioreg.ByName("GPIO17")
	dcPin := gpioreg.ByName("GPIO25")
	csPin := gpioreg.ByName("GPIO8")
	busyPin := gpioreg.ByName("GPIO24")
	pwrPin := gpioreg.ByName("GPIO18")

	fmt.Printf("RST: %v, DC: %v, CS: %v, BUSY: %v, PWR: %v\n", rstPin, dcPin, csPin, busyPin, pwrPin)

	if rstPin == nil || dcPin == nil || csPin == nil || busyPin == nil || pwrPin == nil {
		return fmt.Errorf("failed to find GPIO pins")
	}

	spiPort, err := spireg.Open("/dev/spidev0.0")
	if err != nil {
		return fmt.Errorf("error opening SPI: %v", err)
	}
	if err := spiPort.LimitSpeed(2 * physic.MegaHertz); err != nil {
		return fmt.Errorf("error setting SPI speed: %v", err)
	}

	conn, err := spiPort.Connect(2*physic.MegaHertz, spi.Mode0, 8)
	if err != nil {
		return fmt.Errorf("error connecting to SPI: %v", err)
	}

	epd = &EPD{
		rstPin:  rstPin,
		dcPin:   dcPin,
		csPin:   csPin,
		busyPin: busyPin,
		pwrPin:  pwrPin,
		spiPort: spiPort,
		conn:    conn,
		Width:   800,
		Height:  480,
	}

	err = epd.init()
	if err != nil {
		return fmt.Errorf("error initializing EPD: %v", err)
	}
	fmt.Println("Waveshare 7.5\" e-ink display (V2) initialized successfully")
	return nil
}

func (e *EPD) init() error {
	e.pwrPin.Out(gpio.High)
	time.Sleep(100 * time.Millisecond)

	e.rstPin.Out(gpio.Low)
	time.Sleep(200 * time.Millisecond)
	e.rstPin.Out(gpio.High)
	time.Sleep(200 * time.Millisecond)

	e.sendCommand(0x12) // Soft reset
	time.Sleep(2 * time.Millisecond)
	for e.busyPin.Read() == gpio.High {
		time.Sleep(10 * time.Millisecond)
	}

	e.sendCommand(0x01) // Driver output control
	e.sendData(0xDF)    // 800-1 = 799 (little-endian: DF 02)
	e.sendData(0x02)
	e.sendData(0x00)

	e.sendCommand(0x03) // Gate driving voltage
	e.sendData(0x00)

	e.sendCommand(0x04) // Source driving voltage
	e.sendData(0x41)
	e.sendData(0xA8)
	e.sendData(0x32)

	e.sendCommand(0x11) // Data entry mode
	e.sendData(0x03)

	e.sendCommand(0x44) // X address start/end
	e.sendData(0x00)
	e.sendData(0x63) // 800/8 - 1 = 99 (0x63)

	e.sendCommand(0x45) // Y address start/end
	e.sendData(0x00)
	e.sendData(0x00)
	e.sendData(0xDF) // 479 (little-endian: DF 01)
	e.sendData(0x01)

	e.sendCommand(0x4E) // X address counter
	e.sendData(0x00)

	e.sendCommand(0x4F) // Y address counter
	e.sendData(0x00)
	e.sendData(0x00)

	return nil
}

func (e *EPD) sendCommand(cmd byte) {
	e.dcPin.Out(gpio.Low)
	if e.conn == nil {
		panic("SPI connection is nil")
	}
	e.conn.Tx([]byte{cmd}, nil)
}

func (e *EPD) sendData(data byte) {
	e.dcPin.Out(gpio.High)
	if e.conn == nil {
		panic("SPI connection is nil")
	}
	e.conn.Tx([]byte{data}, nil)
}

func (e *EPD) sendData2(buffer []byte) error {
	const maxTxSize = 4096
	e.dcPin.Out(gpio.High)
	if e.conn == nil {
		return fmt.Errorf("SPI connection is nil")
	}
	for i := 0; i < len(buffer); i += maxTxSize {
		end := i + maxTxSize
		if end > len(buffer) {
			end = len(buffer)
		}
		chunk := buffer[i:end]
		err := e.conn.Tx(chunk, nil)
		if err != nil {
			return fmt.Errorf("error sending buffer chunk %d-%d: %v", i, end, err)
		}
	}
	return nil
}

func cleanupDisplay() {
	if epd != nil {
		epd.sleep()
		epd.spiPort.Close()
		fmt.Println("Waveshare 7.5\" e-ink display put to sleep")
	}
}

func (e *EPD) sleep() {
	e.sendCommand(0x10) // Deep sleep
	e.sendData(0x01)
	time.Sleep(200 * time.Millisecond)
	e.pwrPin.Out(gpio.Low)
}

func clearDisplay() {
	fmt.Println("Clearing e-ink display...")
	buffer := make([]byte, 800*480/8)
	for i := range buffer {
		buffer[i] = 0xFF // White
	}
	err := epd.display(buffer)
	if err != nil {
		fmt.Printf("Error clearing display: %v\n", err)
	}
	time.Sleep(2 * time.Second)
}

func testDisplay() {
	fmt.Println("Testing display with pattern...")
	buffer := make([]byte, 800*480/8)
	for i := 0; i < len(buffer)/2; i++ {
		buffer[i] = 0x00 // Black
	}
	for i := len(buffer)/2; i < len(buffer); i++ {
		buffer[i] = 0xFF // White
	}
	err := epd.display(buffer)
	if err != nil {
		fmt.Printf("Error testing display: %v\n", err)
	}
	time.Sleep(2 * time.Second)
}

func (e *EPD) display(buffer []byte) error {
	// Create inverted buffer (image1)
	image1 := make([]byte, len(buffer))
	for i := range buffer {
		image1[i] = ^buffer[i] // Bitwise NOT
	}

	// Send old data (inverted)
	e.sendCommand(0x10)
	err := e.sendData2(image1)
	if err != nil {
		return fmt.Errorf("error sending old data: %v", err)
	}

	// Send new data
	e.sendCommand(0x13)
	err = e.sendData2(buffer)
	if err != nil {
		return fmt.Errorf("error sending new data: %v", err)
	}

	// Refresh
	e.sendCommand(0x12)
	time.Sleep(100 * time.Millisecond)
	for e.busyPin.Read() == gpio.High {
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

func processNextImage(tmpDir, apiKey string, options AppOptions) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered from panic: %v\n", r)
			time.Sleep(60 * time.Second)
		}
	}()

	req, err := http.NewRequest("GET", "https://usetrmnl.com/api/display", nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		time.Sleep(60 * time.Second)
		return
	}

	req.Header.Add("access-token", apiKey)
	req.Header.Add("User-Agent", fmt.Sprintf("trmnl-display/%s", version))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error fetching display: %v\n", err)
		time.Sleep(60 * time.Second)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Error fetching display: status code %d\n", resp.StatusCode)
		time.Sleep(60 * time.Second)
		return
	}

	var terminal TerminalResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&terminal); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		time.Sleep(60 * time.Second)
		return
	}

	filename := terminal.Filename
	if filename == "" {
		filename = "display.jpg"
	}
	filePath := filepath.Join(tmpDir, filename)

	imgResp, err := http.Get(terminal.ImageURL)
	if err != nil {
		fmt.Printf("Error downloading image: %v\n", err)
		time.Sleep(60 * time.Second)
		return
	}
	defer imgResp.Body.Close()

	out, err := os.Create(filePath)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		time.Sleep(60 * time.Second)
		return
	}
	_, err = io.Copy(out, imgResp.Body)
	if err != nil {
		fmt.Printf("Error saving image: %v\n", err)
		out.Close()
		time.Sleep(60 * time.Second)
		return
	}
	out.Close()

	err = displayImage(filePath, options)
	if err != nil {
		fmt.Printf("Error displaying image: %v\n", err)
		time.Sleep(60 * time.Second)
		return
	}

	refreshRate := terminal.RefreshRate
	if refreshRate <= 0 {
		refreshRate = 60
	}
	time.Sleep(time.Duration(refreshRate) * time.Second)
}

func displayImage(imagePath string, options AppOptions) error {
	if options.Verbose {
		fmt.Printf("Reading image from %s\n", imagePath)
	}

	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("error opening image file for detection: %v", err)
	}
	defer file.Close()

	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil && err != io.EOF {
		return fmt.Errorf("error reading image for detection: %v", err)
	}
	contentType := http.DetectContentType(buffer)

	_, err = file.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("error resetting file pointer: %v", err)
	}

	var imgPath string
	if contentType == "image/bmp" {
		pngPath := imagePath + ".png"
		cmd := exec.Command("convert", imagePath, pngPath)
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("error converting BMP to PNG with convert: %v", err)
		}
		defer os.Remove(pngPath)
		imgPath = pngPath
	} else {
		imgPath = imagePath
	}

	imgFile, err := os.Open(imgPath)
	if err != nil {
		return fmt.Errorf("error opening image file: %v", err)
	}
	defer imgFile.Close()

	img, _, err := image.Decode(imgFile)
	if err != nil {
		return fmt.Errorf("error decoding image: %v", err)
	}

	resizedImg := imaging.Resize(img, epd.Width, epd.Height, imaging.NearestNeighbor)

	monoImg := image.NewGray(resizedImg.Bounds())
	threshold := uint8(128)
	for y := 0; y < resizedImg.Bounds().Dy(); y++ {
		for x := 0; x < resizedImg.Bounds().Dx(); x++ {
			r, g, b, _ := resizedImg.At(x, y).RGBA()
			gray := uint8((r*299 + g*587 + b*114) / 1000 >> 8)
			if options.DarkMode {
				if gray < threshold {
					monoImg.SetGray(x, y, color.Gray{255}) // White
				} else {
					monoImg.SetGray(x, y, color.Gray{0})   // Black
				}
			} else {
				if gray < threshold {
					monoImg.SetGray(x, y, color.Gray{0})   // Black
				} else {
					monoImg.SetGray(x, y, color.Gray{255}) // White
				}
			}
		}
	}

	// Convert to buffer (Black=0, White=1)
	buffer := make([]byte, epd.Width*epd.Height/8)
	for y := 0; y < epd.Height; y++ {
		for x := 0; x < epd.Width; x++ {
			gray := monoImg.GrayAt(x, y).Y
			bitPos := y*epd.Width + x
			bytePos := bitPos / 8
			bitOffset := uint(7 - (bitPos % 8))
			if gray == 0 { // Black
				buffer[bytePos] &^= (1 << bitOffset) // Clear bit (0)
			} else { // White
				buffer[bytePos] |= (1 << bitOffset) // Set bit (1)
			}
		}
	}

	err = imaging.Save(monoImg, "debug_buffer.png")
	if err != nil {
		return fmt.Errorf("error saving debug buffer image: %v", err)
	}
	if options.Verbose {
		fmt.Println("Saved debug_buffer.png for inspection")
	}

	err = epd.display(buffer)
	if err != nil {
		return fmt.Errorf("error displaying buffer: %v", err)
	}

	if options.Verbose {
		fmt.Println("Image displayed on Waveshare 7.5\" e-ink display")
	}
	return nil
}

func parseCommandLineArgs() AppOptions {
	darkMode := flag.Bool("d", false, "Enable dark mode (invert monochrome images)")
	showVersion := flag.Bool("v", false, "Show version information")
	verbose := flag.Bool("verbose", true, "Enable verbose output")
	quiet := flag.Bool("q", false, "Quiet mode (disable verbose output)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("trmnl-display version %s (commit: %s, built: %s)\n", version, commit, buildDate)
		os.Exit(0)
	}

	return AppOptions{
		DarkMode: *darkMode,
		Verbose:  *verbose && !*quiet,
	}
}

func loadConfig(configDir string) Config {
	configFile := filepath.Join(configDir, "config.json")
	config := Config{}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return config
	}
	_ = json.Unmarshal(data, &config)
	return config
}

func saveConfig(configDir string, config Config) {
	configFile := filepath.Join(configDir, "config.json")
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		return
	}
	err = os.WriteFile(configFile, data, 0600)
	if err != nil {
		fmt.Printf("Error writing config file: %v\n", err)
	}
}