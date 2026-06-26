#pragma once
#include <Wire.h> 

#define I2C_SCL_PIN       7
#define I2C_SDA_PIN       15

extern TwoWire I2C;

void I2C_Init(void);
bool I2C_Read(uint8_t Driver_addr, uint16_t Reg_addr, uint8_t *Reg_data, uint32_t Length);
bool I2C_Write(uint8_t addr, uint16_t reg_addr, const uint8_t *reg_data, uint32_t length);