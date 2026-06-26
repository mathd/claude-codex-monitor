/*Using LVGL with Arduino requires some extra steps:
 *Be sure to read the docs here: https://docs.lvgl.io/master/get-started/platforms/arduino.html  */

#include "Wireless.h"
#include "Gyro_QMI8658.h"
#include "RTC_PCF85063.h"
#include "SD_Card.h"
#include "LVGL_Driver.h"
#include "LVGL_Example.h"
void Driver_Init()
{
  Flash_test();
  I2C_Init();
  TCA9554PWR_Init(0x00);   
  Set_EXIO(EXIO_PIN8,Low);
  Backlight_Init();
  Set_Backlight(50);      //0~100 
}
void setup()
{
  Wireless_Test2();
  Driver_Init();
  RTC_Init();
  QMI8658_Init(); 
  LCD_Init();                                     // If you later reinitialize the LCD, you must initialize the SD card again !!!!!!!!!!
  SD_Init();                                      // It must be initialized after the LCD, and if the LCD is reinitialized later, the SD also needs to be reinitialized
  Lvgl_Init();

  Lvgl_Example1();
  // lv_demo_widgets();               
  // lv_demo_benchmark();          
  // lv_demo_keypad_encoder();     
  // lv_demo_music();              
  // lv_demo_printer();
  // lv_demo_stress();   
}

void loop()
{
  Lvgl_Loop();
  QMI8658_Loop();
  RTC_Loop();
  vTaskDelay(pdMS_TO_TICKS(5));
}
