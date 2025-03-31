#!/usr/bin/env python3
import smbus
import struct
import time

bus = smbus.SMBus(1)  # I2C bus 1
ADDRESS = 0x36

def PowerOnReset(bus):
    bus.write_word_data(ADDRESS, 0xfe, 0x0054)

def readVoltage(bus):
    read = bus.read_word_data(ADDRESS, 0x02)
    swapped = struct.unpack("<H", struct.pack(">H", read))[0]
    voltage = swapped * 1.25 / 1000 / 16
    return voltage

PowerOnReset(bus)
print(f"Voltage: {readVoltage(bus):.1f}V")
time.sleep(1)