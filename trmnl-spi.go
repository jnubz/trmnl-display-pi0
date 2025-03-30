package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // Register JPEG decoder
	_ "image/png"  // Register PNG decoder
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/disintegration/imaging" // For image processing
	// Replace with your specific e-ink display library, e.g., github.com/elecnix/epd or Waveshare's library
	// For this example, I'll use a placeholder "epd" package
	"epd" // Hypothetical import; replace with actual library like "github.com/waveshare/e-Paper/Go"
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

// SPIConfig holds SPI and GPIO pin configuration for the e-ink display
type SPIConfig struct {
	Device     string // e.g., "/dev/spidev0.0"
	RSTPin     int    // Reset pin
	DCPin      int    // Data/Command pin
	BusyPin    int    // Busy pin
	CSPin      int    // Chip Select pin (optional)
	Width      int    // Display width in pixels
	Height     int    // Display height in pixels
}

var (
	// Global SPI configuration (adjust based on your display and wiring)
	spiConfig = SPIConfig{
		Device:  "/dev/spidev0.0",
		RSTPin:  17, // GPIO17
		DCPin:   25, // GPIO25
		BusyPin: 24, // GPIO24
		CSPin:   8,  // GPIO8 (optional)
		Width:   250, // Example: 2.13" Waveshare display is 250x122
		Height:  122,
	}
	// Global e-ink display driver instance
	display *epd.EPD // Replace with your actual driver type
)

func main() {
	// Check root privileges
	checkRoot()

	// Parse command line arguments
	options := parseCommandLineArgs()

	// Set up signal handling for clean exit
	setupSignalHandling()

	// Initialize the e-ink display
	err := initDisplay()
	if err != nil {
		fmt.Printf("Error initializing e-ink display: %v\n", err)
		os.Exit(1)
	}
	defer cleanupDisplay()

	// Create a configuration directory
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

	// Get API key
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

	// Create a temporary directory for storing images
	tmpDir, err := os.MkdirTemp("", "trmnl-display")
	if err != nil {
		fmt.Printf("Error creating temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Clear the display at startup
	clearDisplay()

	for {
		processNextImage(tmpDir, config.APIKey, options)
	}
}

// initDisplay initializes the SPI-connected e-ink display
func initDisplay() error {
	// Replace with actual initialization based on your library
	// Example for a Waveshare-like display:
	display = epd.NewEPD(spiConfig.Device, spiConfig.RSTPin, spiConfig.DCPin, spiConfig.BusyPin, spiConfig.CSPin, spiConfig.Width, spiConfig.Height)
	err := display.Init()
	if err != nil {
		return fmt.Errorf("failed to initialize e-ink display: %v", err)
	}
	fmt.Println("E-ink display initialized successfully")
	return nil
}

// cleanupDisplay handles cleanup on exit
func cleanupDisplay() {
	if display != nil {
		display.Sleep() // Put display to sleep
		fmt.Println("E-ink display put to sleep")
	}
}

// clearDisplay clears the e-ink display
func clearDisplay() {
	fmt.Println("Clearing e-ink display...")
	err := display.Clear()
	if err != nil {
		fmt.Printf("Error clearing display: %v\n", err)
	}
}

// setupSignalHandling sets up handlers for SIGINT, SIGTERM, and SIGHUP
func setupSignalHandling() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-c
		fmt.Println("\nReceived termination signal. Cleaning up...")
		cleanupDisplay()
		os.Exit(0)
	}()
}

// processNextImage handles fetching and displaying images
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

// displayImage processes and sends the image to the e-ink display
func displayImage(imagePath string, options AppOptions) error {
	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("error opening image file: %v", err)
	}
	defer file.Close()

	if options.Verbose {
		fmt.Printf("Reading image from %s\n", imagePath)
	}

	img, _, err := image.Decode(file)
	if err != nil {
		return fmt.Errorf("error decoding image: %v", err)
	}

	// Resize image to match display dimensions
	resizedImg := imaging.Resize(img, spiConfig.Width, spiConfig.Height, imaging.NearestNeighbor)

	// Convert to monochrome (e-ink displays are typically 1-bit)
	monoImg := image.NewGray(resizedImg.Bounds())
	threshold := uint8(128) // Adjust threshold as needed
	for y := 0; y < resizedImg.Bounds().Dy(); y++ {
		for x := 0; x < resizedImg.Bounds().Dx(); x++ {
			r, g, b, _ := resizedImg.At(x, y).RGBA()
			gray := uint8((r*299 + g*587 + b*114) / 1000 >> 8) // ITU-R 601-2 luma transform
			if options.DarkMode {
				if gray < threshold {
					monoImg.SetGray(x, y, color.Gray{255})
				} else {
					monoImg.SetGray(x, y, color.Gray{0})
				}
			} else {
				if gray >= threshold {
					monoImg.SetGray(x, y, color.Gray{255})
				} else {
					monoImg.SetGray(x, y, color.Gray{0})
				}
			}
		}
	}

	// Send to e-ink display
	err = display.Display(monoImg)
	if err != nil {
		return fmt.Errorf("error displaying image on e-ink: %v", err)
	}

	if options.Verbose {
		fmt.Println("Image displayed on e-ink display")
	}
	return nil
}

// checkRoot verifies root privileges
func checkRoot() {
	currentUser, err := user.Current()
	if err != nil {
		fmt.Printf("Error determining current user: %v\n", err)
		os.Exit(1)
	}
	if currentUser.Uid != "0" {
		fmt.Println("This program requires root privileges to access SPI.")
		fmt.Println("Please run with sudo or as root.")
		os.Exit(1)
	}
	fmt.Println("Running with root privileges ✓")
}

// parseCommandLineArgs parses command line arguments
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

// Other helper functions (loadConfig, saveConfig) remain unchanged
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