#!/usr/bin/env python3

import sys
sys.path.append('/usr/local/lib/python3.9/dist-packages')  # Adjust for your Waveshare lib path
from waveshare_epd import epd7in5_V2  # Adjust to your e-paper model
import time
from PIL import Image, ImageDraw, ImageFont
import struct
import smbus
import RPi.GPIO as GPIO

# Initialize I2C bus for UPS Lite (MAX17040)
bus = smbus.SMBus(1)  # I2C bus 1 on Pi Zero
ADDRESS = 0x36

# UPS Lite functions
def readVoltage(bus):
    """Returns battery voltage in volts"""
    read = bus.read_word_data(ADDRESS, 0x02)
    swapped = struct.unpack("<H", struct.pack(">H", read))[0]
    voltage = swapped * 1.25 / 1000 / 16
    return voltage

def readCapacity(bus):
    """Returns battery capacity in percentage"""
    read = bus.read_word_data(ADDRESS, 0x04)
    swapped = struct.unpack("<H", struct.pack(">H", read))[0]
    capacity = swapped / 256
    return capacity

def QuickStart(bus):
    bus.write_word_data(ADDRESS, 0x06, 0x4000)

def PowerOnReset(bus):
    bus.write_word_data(ADDRESS, 0xfe, 0x0054)

# GPIO setup for power adapter detection
GPIO.setmode(GPIO.BCM)
GPIO.setwarnings(False)
GPIO.setup(4, GPIO.IN)

# Initialize UPS Lite
PowerOnReset(bus)
QuickStart(bus)

# Initialize e-paper
epd = epd7in5_V2.EPD()
epd.init()
epd.Clear()

# Main loop
while True:
    # Create image
    image = Image.new('1', (epd.width, epd.height), 255)  # White background
    draw = ImageDraw.Draw(image)
    font = ImageFont.truetype('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf', 20)

    # Get battery data
    voltage = readVoltage(bus)
    capacity = readCapacity(bus)
    adapter_status = "Plugged In" if GPIO.input(4) == GPIO.HIGH else "Unplugged"

    # Draw text
    draw.text((10, 10), f"Voltage: {voltage:.1f}V", font=font, fill=0)
    draw.text((10, 40), f"Battery: {int(capacity)}%", font=font, fill=0)
    draw.text((10, 70), f"Adapter: {adapter_status}", font=font, fill=0)

    # Display
    epd.display(epd.getbuffer(image))

    # Sleep (update every minute)
    time.sleep(60)

# Cleanup (unreachable in loop, but good practice)
epd.sleep()
GPIO.cleanup()