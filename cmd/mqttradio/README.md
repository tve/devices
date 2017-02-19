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
topic that ends in the radio node id. It subscribes to a separate
set of topics for Tx.

## MQTT

For each radio an MQTT topic prefix may be specified. A packet received from node N
is published to topic `<prefix>/rx/<N>` with a payload consisting of the packet payload without
headers or trailers or CRC. The GW subscribes to topics `<prefix>/tx/+` and transmits
packets to the node identified by the last element of the topic.

## LoRa protocol

The protocol used for LoRa is the JeeLabs LoRa (JLL) protocol & packet format. See
`jll.go` in the sx1276 driver. The gateway responds to incoming data packets requesting an ACK
with an immediate ACK. The gateway sends packets out without requesting an ACK.
(The plan is to queue outgoing packets until the node polls the GW, but this is not yet
implemented.)

## FSK protocol

The protocol used for FSK is the JeeLabs packet format and ACK protocol. Details TBD...
