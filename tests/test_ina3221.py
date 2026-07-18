import unittest

from system_helper import Ina3221Direct


def swap_word(value):
    return ((value << 8) & 0xFF00) | ((value >> 8) & 0x00FF)


class FakeSmbus:
    def __init__(self, registers=None):
        self.registers = registers or {}
        self.writes = []
        self.closed = False

    def write_word_data(self, address, register, value):
        self.writes.append((address, register, value))

    def read_word_data(self, address, register):
        del address
        return self.registers[register]

    def close(self):
        self.closed = True


class Ina3221DirectTests(unittest.TestCase):
    def test_channel_1_reads_fl2c_full_scale_current(self):
        bus = FakeSmbus(
            {
                0x01: swap_word(0x3A98),  # 1875 * 40 uV = 75 mV
                0x02: swap_word(0x2EE0),  # 1500 * 8 mV = 12 V
            }
        )
        sensor = Ina3221Direct(
            bus_number=1,
            address=0x40,
            channel=1,
            shunt_ohms=0.00375,
            bus=bus,
        )

        voltage, current_ma = sensor.read()

        self.assertEqual(bus.writes, [(0x40, 0x00, swap_word(0x4527))])
        self.assertAlmostEqual(voltage, 12.0)
        self.assertAlmostEqual(current_ma, 20000.0)

    def test_channel_3_preserves_negative_shunt_direction(self):
        bus = FakeSmbus(
            {
                0x05: swap_word(0xE890),  # -750 * 40 uV = -30 mV
                0x06: swap_word(0x2580),  # 1200 * 8 mV = 9.6 V
            }
        )
        sensor = Ina3221Direct(
            bus_number=1,
            address=0x40,
            channel=3,
            shunt_ohms=0.00375,
            bus=bus,
        )

        voltage, current_ma = sensor.read()

        self.assertEqual(bus.writes, [(0x40, 0x00, swap_word(0x1527))])
        self.assertAlmostEqual(voltage, 9.6)
        self.assertAlmostEqual(current_ma, -8000.0)

    def test_reads_battery_and_output_channels_with_identical_shunts(self):
        bus = FakeSmbus(
            {
                0x01: swap_word(0x02F0),  # 94 * 40 uV = 3.76 mV
                0x02: swap_word(0x2EE0),  # 1500 * 8 mV = 12 V
                0x03: swap_word(0x1770),  # 750 * 40 uV = 30 mV
                0x04: swap_word(0x4A38),  # 2375 * 8 mV = 19 V
            }
        )
        sensor = Ina3221Direct(
            bus_number=1,
            address=0x40,
            channel=1,
            shunt_ohms=0.00375,
            enabled_channels={1, 2},
            bus=bus,
        )

        battery_voltage, battery_current_ma = sensor.read()
        output_voltage, output_current_ma = sensor.read_channel(2, 0.00375)

        self.assertEqual(bus.writes, [(0x40, 0x00, swap_word(0x6527))])
        self.assertAlmostEqual(battery_voltage, 12.0)
        self.assertAlmostEqual(battery_current_ma, 1002.6666667)
        self.assertAlmostEqual(output_voltage, 19.0)
        self.assertAlmostEqual(output_current_ma, 8000.0)

    def test_rejects_invalid_channel_and_shunt(self):
        with self.assertRaises(ValueError):
            Ina3221Direct(1, 0x40, 0, 0.00375, bus=FakeSmbus())
        with self.assertRaises(ValueError):
            Ina3221Direct(1, 0x40, 1, 0.0, bus=FakeSmbus())
        with self.assertRaises(ValueError):
            Ina3221Direct(
                1,
                0x40,
                1,
                0.00375,
                enabled_channels={1, 4},
                bus=FakeSmbus(),
            )
        with self.assertRaises(ValueError):
            Ina3221Direct(
                1,
                0x40,
                1,
                0.00375,
                averaging_samples=2,
                bus=FakeSmbus(),
            )

    def test_closes_injected_bus(self):
        bus = FakeSmbus()
        sensor = Ina3221Direct(1, 0x40, 1, 0.00375, bus=bus)

        sensor.close()

        self.assertTrue(bus.closed)


if __name__ == "__main__":
    unittest.main()
