#!/usr/bin/env python3

import os
import sys
import time
import json
import requests
from io import BytesIO
from PIL import Image
import argparse
import logging
import tempfile

try:
    from waveshare_epd import epd7in5_V2
except ImportError:
    print("Error: Waveshare EPD library not found. Please ensure epd7in5_V2.py is available.")
    sys.exit(1)

VERSION = "0.1.0"
COMMIT = "unknown"
BUILD_DATE = "unknown"

CONFIG_DIR = os.path.expanduser("~/.trmnl")
CONFIG_FILE = os.path.join(CONFIG_DIR, "config.json")

def load_config():
    if os.path.exists(CONFIG_FILE):
        with open(CONFIG_FILE, 'r') as f:
            config = json.load(f)
            return config.get("api_key", "")
    return os.getenv("TRMNL_API_KEY", "")

def save_config(api_key):
    os.makedirs(CONFIG_DIR, exist_ok=True)
    config = {"api_key": api_key}
    with open(CONFIG_FILE, 'w') as f:
        json.dump(config, f, indent=2)

def get_api_key():
    api_key = load_config()
    if not api_key:
        api_key = input("TRMNL API Key not found. Please enter your TRMNL API Key: ")
        save_config(api_key)
    logging.debug(f"Loaded API key: {api_key}")
    return api_key

def init_display():
    try:
        epd = epd7in5_V2.EPD()
        epd.init()
        logging.info("Waveshare 7.5\" e-ink display (V2) initialized successfully")
        return epd
    except Exception as e:
        logging.error(f"Error initializing e-ink display: {e}")
        sys.exit(1)

def clear_display(epd):
    logging.info("Clearing e-ink display...")
    epd.Clear()

def test_display(epd):
    logging.info("Testing display with pattern...")
    width, height = epd.width, epd.height
    buffer = bytearray(width * height // 8)
    for i in range(len(buffer) // 2):
        buffer[i] = 0x00  # Black
    for i in range(len(buffer) // 2, len(buffer)):
        buffer[i] = 0xFF  # White
    epd.display(buffer)
    time.sleep(2)

def process_image(epd, api_key, dark_mode, verbose):
    headers = {
        "access-token": api_key,
        "User-Agent": f"trmnl-display/{VERSION}"
    }
    logging.info("Fetching image from TRMNL API...")
    try:
        response = requests.get("https://usetrmnl.com/api/display", headers=headers, timeout=30)
        response.raise_for_status()
        logging.debug(f"API response status: {response.status_code}")
    except requests.RequestException as e:
        logging.error(f"Error fetching display: {e}")
        time.sleep(60)
        return

    try:
        data = response.json()
        logging.debug(f"Raw API response: {data}")
        image_url = data["image_url"]
        filename = data.get("filename", "display.jpg")
        refresh_rate = data.get("refresh_rate", 60)
        logging.info(f"API parsed: url={image_url}, filename={filename}, refresh={refresh_rate}")
    except (json.JSONDecodeError, KeyError) as e:
        logging.error(f"Error parsing JSON: {e}")
        time.sleep(60)
        return

    with tempfile.TemporaryDirectory() as tmp_dir:
        file_path = os.path.join(tmp_dir, filename)
        try:
            logging.info(f"Downloading image from {image_url}")
            img_response = requests.get(image_url, timeout=30)
            img_response.raise_for_status()
            with open(file_path, 'wb') as f:
                f.write(img_response.content)
            logging.debug(f"Image saved to {file_path}")
        except requests.RequestException as e:
            logging.error(f"Error downloading image: {e}")
            time.sleep(60)
            return

        if verbose:
            logging.info(f"Reading image from {file_path}")

        try:
            img = Image.open(file_path).convert('L')
            img = img.resize((epd.width, epd.height), Image.NEAREST)
            mono_img = Image.new('1', (epd.width, epd.height))
            threshold = 128
            for y in range(epd.height):
                for x in range(epd.width):
                    pixel = img.getpixel((x, y))
                    if dark_mode:
                        mono_img.putpixel((x, y), 255 if pixel < threshold else 0)
                    else:
                        mono_img.putpixel((x, y), 0 if pixel < threshold else 255)

            mono_img.save("debug_buffer.png")
            if verbose:
                logging.info("Saved debug_buffer.png for inspection")

            buffer = bytearray(epd.width * epd.height // 8)
            for y in range(epd.height):
                for x in range(epd.width):
                    bit_pos = y * epd.width + x
                    byte_pos = bit_pos // 8
                    bit_offset = 7 - (bit_pos % 8)
                    if mono_img.getpixel((x, y)) == 0:  # Black
                        buffer[byte_pos] &= ~(1 << bit_offset)
                    else:  # White
                        buffer[byte_pos] |= (1 << bit_offset)

            logging.info("Displaying image...")
            epd.display(buffer)
            if verbose:
                logging.info("Image displayed on Waveshare 7.5\" e-ink display")
            time.sleep(max(refresh_rate, 1))
        except Exception as e:
            logging.error(f"Error displaying image: {e}")
            time.sleep(60)

def main():
    parser = argparse.ArgumentParser(description="TRMNL e-ink display client")
    parser.add_argument("-d", "--dark-mode", action="store_true", help="Enable dark mode (invert monochrome images)")
    parser.add_argument("-v", "--version", action="store_true", help="Show version information")
    parser.add_argument("--verbose", action="store_true", help="Enable verbose output")
    parser.add_argument("-q", "--quiet", action="store_true", help="Quiet mode (disable verbose output)")
    args = parser.parse_args()

    if args.version:
        print(f"trmnl-display version {VERSION} (commit: {COMMIT}, built: {BUILD_DATE})")
        sys.exit(0)

    log_level = logging.INFO if args.verbose and not args.quiet else logging.WARNING
    logging.basicConfig(level=log_level, format="%(message)s")
    logging.getLogger().setLevel(log_level)

    api_key = get_api_key()
    epd = init_display()

    try:
        clear_display(epd)
        test_display(epd)
        while True:
            process_image(epd, api_key, args.dark_mode, args.verbose and not args.quiet)
    finally:
        epd.sleep()
        logging.info("Waveshare 7.5\" e-ink display put to sleep")
        epd.epdconfig.module_exit()

if __name__ == "__main__":
    main()