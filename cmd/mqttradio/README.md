# MQTT Radio - LoRa and FSK gateway to MQTT

mqttradio gateways between MQTT and an sx1276 LoRa radio or an sx1231
FSK radio.  Typical modules for the sx1276 are the HopeRF rfm95, rfm96,
rfm97, and rfm98, as well as the Dorji drf1278. Typical modules for the
sx1231 are the HopeRf rfm69CW, rfm69HCW, rfm69W, and rfm69HW.

On the LoRa side the gateway does _not_ implement or support the LoRaWAN
protocols. It uses LoRa as a simple packet radio and implements its own
protocol, which consists of a simple header and a simple ACK scheme.

On the FSK side the gateway uses the JeeLabs packet format and ACK
protocol, although this is really fully configurable using different
protocol modules.

## MQTT

The MQTT interface is very simple. Packets received end up in one
or multiple topics and the GW subscribes to one or multiple topics
for packets to be sent. The "multiple" aspect comes in when there are
protocol modules layered on top of the raw radio transport: each module
can have its own tx or rx topic making it possible to interact with the
GW at different levels of abstraction.

Currently all MQTT messages use JSON formatting, i.e. typically consist
of a JSON hash with a base64 encoded string for the packet payload and
additional fields for metadata or decoded data (the details really depend
on the protocol modules used).

In the configuration the MQTT topics are specified mostly as
prefixes.  The radios as well as many protocol modules add suffixes as
appropriate. For example, the radios add a /tx and a /rx suffix while
some protocol decoders might add a /N suffix where N is the id of the
remote node.

## Raw radio layer

The implementation of the GW consists of two parts and of an optimization
which ties them together efficiently. The first part is tha raw radio
layer. It manages the radios and relays incoming packets into raw packets
published to an MQTT topic, and it subscribes to a topic where it expects
to receive raw packets and transmits them. The raw packets consist of
a byte array and a number of metadata fields, such as RSSI and SNR on
the receiving end and SYNC length on the transmit end.

## Protocol modules

The second part of the GW is a set of pluggable protocol modules. Each
module subscribes to an MQTT topic where it receives packets,
transforms them, and publishes the result to another MQTT topic. For
example, a decoder can subscribe to the raw received packets, decode the
packet format, and publish the packet data in a decoded format to MQTT.
Another example module is an ACK generator. It subscribes to raw received
packets, determines whether an ACK is needed, and publishes ACKs to the
raw transmission topic.

## Real-time performance

The optimization alluded to earlier is that the GW short-circuits the
round-trip to the MQTT broker when one module (or a radio) publishes a
message to a topic that another module subscribes to. To take the ACK
example, when the radio layer publishes the raw packet to MQTT the GW
actually first calls the ACK generator module with the packet and only
later performs the MQTT publication. If the ACK generator publishes an
ACK to the raw TX topic the GW actually first calls the radio transmit
function and only later performs the MQTT publishing.

This short-circuiting of the packet/message forwaring is purely
an optimization. Everything still goes through the MQTT broker,
eventually. It would also be possible for some external process to
publish a message that looks like a raw incoming packet to the radio's
RX topic, making it look as if that packet had just been received,
and the ACK generator module will pick it up without being able to tell
the difference.

Note that in order to avoid duplicate message delivery (once direct
and then again via MQTT) the GW performs duplicate packet detection,
which uses a simple but not perfect algorithm. There is also a slight
difference between internally forwarded messages and ones going via
the broker because the json marshalling an unmarshalling is skipped,
which can uncover inconsistencies if the Go types used by two modules
are not identical but json-compatible.

In terms of real-time performance, the goroutines that handle
incoming radio packets run at a real-time scheduling level at the
Linux kernel-thread level. This effectively means that they have
highest priority in the system and are not interrupted by pretty much
anything else. In addition, the internal packet forwarding is all done
via function calls on the same goroutine. So in the above example the
ACK generation as well as the resulting ACK transmission happen on the
same goroutine as the initial reception. The result is a very fast and
consistent turn-around time.

Note that the real-time scheduling priority is orthogonal to the use of a
Linux kernel with the RT-PREEMPT ("real-time") patch. The RT-PREEMPT patch
allows the kernel to be interrupted in more places with the effect that
interrupt latency is reduced and more consistent. This certainly helps
here but is independent of the real-time kernel thread scheduling concept.

## Configuration

The GW is configured using a toml config file, canonically called
`mqttradio.toml`.  In that config file radio interfaces are instatiated
and assigned mqtt topics for transmission and reception and similarly
protocol modules are instatiated and hooked to input and output
topics. When the GW starts it instantiates all the pieces, issues MQTT
subscriptions, and thereafter the flow of thing is dictated by radio
events as MQTT message delivery.

The sample mqttradio.toml config file contains many comments and is
hopefully self-explanatory.

## Attaching radios

The radios are assumed to be attached to the system using the periph
HW interface library. The supported radios use the SPI bus plus a GPIO
interrupt pin. This means that the radios must be connected to one of
the system's SPI buses for which there is a linux driver, i.e., that
shows up as /dev/spiN.M (technically, if periph supports a different
way to access SPI that will be usable too).

The GPIO interrupt pin must be capable of actually generating an hardware
interrupt. On many platforms not all GPIO pins are capable of doing so.

In order to be able to attach multiple radios on platforms that only have
a single SPI bus with a single chip=select mqttradio supports the use
of a chip-select mux. The idea is to use a 1-2 demultiplexer, such as a
74LVC1G19, and feed the bus' chip-select line through it to two radios
selected by an additional GPIO pin. This extra mux pin can be specified
in the config together with its value (0 or 1) for each of the two radios.

## Code strucure

- `main.go` contains the config file parsing and general set-up of all the pieces
- `raw.go` contains the interfaces to the radios as well as the data types
  used for raw radio packets. It is here that the goroutines (one per radio)
  that receive packets are created.
- `mqtt.go` contains the code to connect to the MQTT broker, publish messages to
  it and subscribe to topics. It also contains the forwarding optimization
  and performs all the JSON marshaling.
- `modules.go` manages protocol modules, which primarily consists of instantiating
  modules according to the config by hooking them into the MQTT pub/sub.
- `jl_proto.go` contains a collection of protocol modules to implement the
  various aspects of the JeeLabs FSK protocol.
- `loragw.go` and `formats.go` do not contain anything useful at the moment.

## Operation

- compile using `go build` or cross-compile using `GOARCH=arm go build`
- run using `sudo ./mqttradio`
- typical platforms include rPi, CHIP, and O-Droid C1+.
