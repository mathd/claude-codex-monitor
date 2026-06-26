#include "RTC_PCF85063.h"

WiFiUDP ntpUDP;
NTPClient timeClient(ntpUDP, "pool.ntp.org");
SensorPCF85063 RTC;
RTC_DateTime datetime;

static uint8_t Time[8] = {0};
char *week[] = {"SUN","Mon","Tues","Wed","Thur","Fri","Sat"};

void RTC_Init(void) {
  if (!RTC.begin(I2C, PCF85063_SLAVE_ADDRESS, I2C_SDA_PIN, I2C_SCL_PIN)) {
    printf("PCF85063 not found - Try again!\r\n");
    if (!RTC.begin(I2C, PCF85063_SLAVE_ADDRESS, I2C_SDA_PIN, I2C_SCL_PIN)) {
      printf("Failed to find PCF85063 !!!\r\n");
      while (1) {
        printf("Failed to find PCF85063 - check your wiring!\r\n");
        vTaskDelay(pdMS_TO_TICKS(1000));
      }
    }
  }
  // Acquisition_time();
}

void PCF85063_ReadTime(void) 
{   
  datetime = RTC.getDateTime();
  printf("%d.%d.%d   %d:%d:%d\r\n",datetime.year,datetime.month,datetime.day,datetime.hour,datetime.minute,datetime.second);
}
void Acquisition_time() {               // Get the network time and set it to PCF85063 to be called after the WIFI connection is successful
  if(WIFI_Connection){   
    printf("WIFI connection successful, time updated!\r\n");
    timeClient.begin();
    timeClient.setTimeOffset(8 * 3600);   // Set the time zone, here use East 8 (Beijing time)
    timeClient.update();

    time_t currentTime = timeClient.getEpochTime();
    while(currentTime < 1609459200)       // Using the current timestamp to compare with a known larger value,1609459200 is a known larger timestamp value that corresponds to January 1, 2021
    {
      timeClient.update();  
      currentTime = timeClient.getEpochTime();
    }
    // Converts the current timestamp to a tm structure
    struct tm *localTime = localtime(&currentTime);
    // Set the network time to PCF85063
    RTC.setDateTime(localTime->tm_year - 100, localTime->tm_mon + 1, localTime->tm_mday, localTime->tm_hour, localTime->tm_min, localTime->tm_sec);
    // Turn off WiFi connection
    // WiFi.disconnect(true);
    // WiFi.mode(WIFI_OFF);
  }
  else{
    printf("WIFI connection failed, time not updated!\r\n");
  }
}

void RTC_Loop(void)
{
  PCF85063_ReadTime();
  // printf("%d%d.%d.%d %s %d:%d:%d\r\n",Time[7],Time[6],Time[5],week[Time[4]],Time[3],Time[2],Time[1],Time[0]);
}