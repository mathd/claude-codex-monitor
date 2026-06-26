#include "I2C_Driver.h"

TwoWire I2C = TwoWire(1);                         

void I2C_Init(void) {
  I2C.begin( I2C_SDA_PIN, I2C_SCL_PIN);                       
}
bool I2C_Read(uint8_t Driver_addr, uint16_t Reg_addr, uint8_t *Reg_data, uint32_t Length)
{
  I2C.beginTransmission(Driver_addr);
  I2C.write((uint8_t)(Reg_addr >> 8));
  I2C.write((uint8_t)Reg_addr);         
  if ( I2C.endTransmission(true)){
    printf("The I2C transmission fails. - I2C Read\r\n");
    return -1;
  }
  I2C.requestFrom(Driver_addr, Length);
  for (int i = 0; i < Length; i++) {
    *Reg_data++ = I2C.read();
  }
  return 0;
}
bool I2C_Write(uint8_t Driver_addr, uint16_t Reg_addr, const uint8_t *Reg_data, uint32_t Length)
{
  I2C.beginTransmission(Driver_addr);
  I2C.write((uint8_t)(Reg_addr >> 8)); 
  I2C.write((uint8_t)Reg_addr);        
  for (int i = 0; i < Length; i++) {
    I2C.write(*Reg_data++);
  }
  if ( I2C.endTransmission(true))
  {
    printf("The I2C transmission fails. - I2C Write\r\n");
    return -1;
  }
  return 0;
}