#pragma once
#include "I2C_Driver.h"
#include <ctime>
#include <WiFi.h>
#include <WiFiUdp.h>
#include <NTPClient.h>
#include "SensorPCF85063.hpp"

#include "Wireless.h"


#define PCF85063_IRQ_PIN  -1

extern TwoWire I2C;
extern RTC_DateTime datetime;

void RTC_Init(void);
void PCF85063_ReadTime(void) ;       
void Acquisition_time(void);        
void RTC_Loop(void);         