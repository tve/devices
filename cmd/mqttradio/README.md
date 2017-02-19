# MQTT Radio - LoRa and FSK gateway to MQTT

mqttradio gateways between MQTT and an sx1276 LoRa radio or an sx1231 FSK radio.
Typical modules for the sx1276 are the HopeRF rfm95, rfm96, rfm97, and rfm98, as well
as the Dorji drf1278. Typical modules for the sx1231 are the HopeRf rfm69CW, rfm69HCW,
rfm69W, and rfm69HW.

On the LoRa side the gateway does _not_ implement or support the LoRaWAN protocols. It
uses LoRa as a simple packet radio and implements its own protocol, which consists of
a simple header and a simple ACK scheme.

On the FSK side the gateway uses the JeeLabs packet format and ACK protocol.

The MQTT interface is very simple and maps packets received from radios to a
topic that ends in the radio node id and packet type. It subscribes to a separate
set of topics for Tx.
