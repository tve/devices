RFM69 Driver Info
=================



Interesting links
-----------------
- Info about listen mode with low duty cycle: https://lowpowerlab.com/forum/low-power-techniques/ultra-low-power-listening-mode-for-battery-nodes/

Notes about the sx1231
----------------------

There are some 20 settings that all interact in undocumented ways,
so getting to a robust driver is tricky.

The biggest area of trouble is around AFC and AGC. What seems to happen is
that once the RSSI threshold is crossed the radio chooses an LNA setting
and a frequency correction.  It is then stuck with that until it decides
to restart RX. The conditions for that restart are not documented, but
experiments show that the RSSI threshold has a big influence, and thus one
has to assume the restart happens when RSSI drops below the threshold.
Once sync match occurs the situation is different and it's the packet
length that drives the state machine.

Some situations that lead to trouble are a weak noise burst that crosses
the RSSI threshold, causes the radio to make a frequency adjustment
and set maximum LNA gain, and then a real packet comes in strongly but
overdrives the receiver due to the LNA gain set to max. A typical symptom
is systematic CRC errors for such packets. Or a real packet comes in
weakly but is missed because AFC got dragged to one side of the center
frequency due to the noise and the packet is at the other side.

A possible solution is to move the RSSI threshold up, but that will
ignore packets that are just above noise and it appears difficult to
auto-adjust the RSSI threshold.

This leaves two primary modes of operation of the radio:
- Minimize interrupts by using interrupt on sync match, let the radio
  restart RX on its own when RSSI gets crossed and no sync match occurs,
  ensure that the RSSI threshold is reasonable and doesn't make the radio lock
  onto background noise for too long, and accept that some packets may
  be missed because the radio is stuck in the wrong AGC/AFC setting due
  to a burst of noise.
- Minimize packet loss by using interrupt on rssi, reset rx after a
  timeout to prevent the radio from being stuck in the wrong AFC/AGC
  setting, accept the fact that there will be many spurious interrupts.

It should be possible to avoid the "AFC/AGC stuck in the wrong setting due
to noise" issue by disabling AFC and AGC. It appears that disabling AFC
causes no frequency error measurement to be made, that makes it impossible
to dynamically tune the radios for crystal freq error. Disabling AGC
means that all transmitters have to adjust their TX power so they all
come in about at the same strength. While these things are certainly
possible they don't look simple either...

This driver ends up using the interrupt-on-rssi approach and resets RX
after 80 byte times have passed (66 bytes max payload plus preamble,
sync bytes, and CRC amount to 70-odd bytes). It tunes the RSSI threshold
every 10 seconds such that the number of interrupts falls between 2.5 and
10 interrupts per second. (This is not expected to be foolproof, alas).

The FEI and AGC measurements can be used to tune the frequencies of two radios
to match, but there are some tricks. If AFC low-beta offset is enabled, the AFC
value reported will include the offset, so that must be subtracted out.
Also, the FEI value reported seems to be after AFC is applied, at least it
doesn't seem usable. Since the AFC value is reset when Rx is restarted by emptying
the FIFO it must be captured before that.

This driver assumes settings with a modulation index >2 and disables the AFC
low-beta offset.
