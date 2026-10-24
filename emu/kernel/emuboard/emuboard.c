// SPDX-License-Identifier: GPL-2.0
/*
 * emuboard - board stubs for running a Yandex speaker rootfs under QEMU "virt".
 *
 * Selected with "emuboard=<board>" on the kernel command line (bergamot, pickle). Without it the
 * driver does nothing. For the selected board it provides, with the names the real kernel uses:
 *
 *   - the root device-tree "compatible" and "model" (so /proc/device-tree/compatible matches);
 *   - LED class devices (max_brightness 255, brightness kept in memory) and no-op "ledfb-*"
 *     LED triggers, plus a writable brightness_scale attribute on the LEDs' parent device;
 *   - input devices with the real names, bus ids and key/switch capabilities, registered in the
 *     real order so /dev/input/eventN numbering matches;
 *   - /sys/class/aml_info/reboot_reason (emuboard.reboot_reason=, default "cold_boot");
 *   - a "ya_pd" power_supply (USB-C PD sink: voltage_now / current_now in mV / mA, health);
 *   - an "ltr308als01" IIO ambient-light device (in_intensity_*);
 *   - the vendor "ledfb" framebuffers (logo, and ring on pickle: N x 1 pixels, 24 bpp RGB) with a
 *     backlight each. Pixels written there are only stored, they do not drive the LEDs.
 *
 * Host-driven tests use /sys/kernel/debug/emuboard/ (see emu/kernel/README.md).
 *
 * This is a test stub, not a hardware model: nothing here drives real hardware.
 */

#define pr_fmt(fmt) "emuboard: " fmt

#include <linux/backlight.h>
#include <linux/ctype.h>
#include <linux/debugfs.h>
#include <linux/device.h>
#include <linux/fb.h>
#include <linux/iio/iio.h>
#include <linux/init.h>
#include <linux/input.h>
#include <linux/kernel.h>
#include <linux/leds.h>
#include <linux/moduleparam.h>
#include <linux/mutex.h>
#include <linux/of.h>
#include <linux/platform_device.h>
#include <linux/power_supply.h>
#include <linux/seq_file.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/uaccess.h>
#include <linux/vmalloc.h>

/* ---------------------------------------------------------------------------------------------- */
/* Board tables                                                                                    */

#define MAX_CODES 8

struct emu_input_def {
	const char *name;
	const char *phys;
	const char *parent;	/* platform device to hang it under, NULL = /devices/virtual */
	u16 bustype, vendor, product, version;
	u16 keys[MAX_CODES];	/* 0-terminated */
	u16 sws[MAX_CODES];	/* 0-terminated; SW_* codes start at 0, so stored +1 */
	bool msc;		/* advertises EV_MSC (MSC_SERIAL, MSC_SCAN) */
};

struct emu_board {
	const char *name;
	const char *model;
	const char *compatible;	/* NUL-separated list */
	int compatible_len;	/* including the final NUL */
	const char *const *triggers;
	void (*gen_leds)(void);
	const struct emu_input_def *inputs;
	struct { const char *label; int leds; } fbs[2];
};

static const char *const bergamot_triggers[] = {
	"ledfb-logo", "ledfb-logo-sync", NULL,
};

static const char *const pickle_triggers[] = {
	"ledfb-logo", "ledfb-logo-sync", "ledfb-ring", "ledfb-ring-sync", NULL,
};

/* From the on-device surveys (/proc/bus/input/devices). */
static const struct emu_input_def bergamot_inputs[] = {
	{ .name = "gpio-keys", .phys = "gpio-keys/input0", .parent = "gpio-keys",
	  .bustype = BUS_HOST, .vendor = 1, .product = 1, .version = 0x100,
	  .keys = { KEY_MUTE }, .sws = { SW_MUTE_DEVICE + 1 } },
	{ .name = "aw933xx", .phys = "",
	  .keys = { KEY_VOLUMEUP, KEY_VOLUMEDOWN, KEY_VOICECOMMAND }, .msc = true },
	{ }
};

static const struct emu_input_def pickle_inputs[] = {
	{ .name = "input_btrcu", .phys = "input_btrcu/input0", .parent = "aml_bt",
	  .bustype = 0x10, .vendor = 1, .product = 1, .version = 0x100,
	  .keys = { KEY_POWER, KEY_COPY } },
	{ .name = "vad_keypad", .phys = "vad_keypad/input3",
	  .bustype = 0x10, .vendor = 1, .product = 1, .version = 0x100,
	  .keys = { KEY_POWER, BTN_0 } },
	{ .name = "mute", .phys = "gpio-keys/input0", .parent = "gpio-keys",
	  .bustype = BUS_HOST, .vendor = 1, .product = 1, .version = 0x100,
	  .keys = { KEY_MUTE }, .sws = { SW_MUTE_DEVICE + 1 } },
	{ .name = "aw933xx", .phys = "",
	  .keys = { KEY_VOLUMEUP, KEY_VOLUMEDOWN, KEY_VOICECOMMAND } },
	{ }
};

/* ---------------------------------------------------------------------------------------------- */
/* State                                                                                           */

struct emu_led {
	struct led_classdev cdev;
	struct list_head list;
	char name[48];
};

static char emu_board_name[16];
static const struct emu_board *board;
static struct platform_device *emu_pdev;
static LIST_HEAD(emu_leds);
static int emu_nleds;
static struct input_dev *emu_inputs[8];
static int emu_ninputs;
static struct dentry *emu_debugfs;
static bool led_log;

static DEFINE_MUTEX(emu_lock);
static char reboot_reason[32] = "cold_boot";
static char brightness_scale[256];
static u32 pd_mv = 12000, pd_ma = 2000;
static u32 lux_raw = 300, lux_gain = 3;
static int lux_itime_us = 100000, lux_offset, lux_scale_micro = 200000;

static int __init emuboard_setup(char *str)
{
	strlcpy(emu_board_name, str, sizeof(emu_board_name));
	return 1;
}
__setup("emuboard=", emuboard_setup);

/* emuboard.reboot_reason=<string>: what /sys/class/aml_info/reboot_reason reports at boot. */
static int reboot_reason_set(const char *val, const struct kernel_param *kp)
{
	strlcpy(reboot_reason, val, sizeof(reboot_reason));
	strim(reboot_reason);
	return 0;
}
static const struct kernel_param_ops reboot_reason_ops = {
	.set = reboot_reason_set,
	.get = NULL,
};
module_param_cb(reboot_reason, &reboot_reason_ops, NULL, 0200);

/* ---------------------------------------------------------------------------------------------- */
/* LEDs                                                                                            */

static void emu_led_set(struct led_classdev *cdev, enum led_brightness b)
{
	if (led_log)
		pr_info_ratelimited("led %s = %d\n", cdev->name, b);
}

static void __init add_led(const char *trigger, const char *fmt, ...)
{
	struct emu_led *l;
	va_list ap;
	int ret;

	l = kzalloc(sizeof(*l), GFP_KERNEL);
	if (!l)
		return;
	va_start(ap, fmt);
	vsnprintf(l->name, sizeof(l->name), fmt, ap);
	va_end(ap);
	l->cdev.name = l->name;
	l->cdev.max_brightness = 255;
	l->cdev.brightness_set = emu_led_set;
	l->cdev.default_trigger = trigger;
	ret = led_classdev_register(&emu_pdev->dev, &l->cdev);
	if (ret) {
		pr_err("led %s: %d\n", l->name, ret);
		kfree(l);
		return;
	}
	list_add_tail(&l->list, &emu_leds);
	emu_nleds++;
}

static void __init gen_clock(const char *prefix, char last_seg)
{
	static const char *const extra[] = { "DH", "DL", "DM", "DEGREE" };
	char seg;
	int d, i;

	for (seg = 'A'; seg <= last_seg; seg++)
		for (d = 0; d < 4; d++)
			add_led(NULL, "%s:white:clock%c%d", prefix, seg, d);
	for (i = 0; i < ARRAY_SIZE(extra); i++)
		add_led(NULL, "%s:white:clock%s", prefix, extra[i]);
}

static void __init gen_bergamot_leds(void)
{
	static const char *const colors[] = { "red", "green", "blue" };
	int c, i;

	for (c = 0; c < 3; c++)
		for (i = 0; i < 6; i++)
			add_led("ledfb-logo", "bergamot:%s:logo%d", colors[c], i);
	add_led("ledfb-logo-sync", "bergamot:logo:sync");
	gen_clock("bergamot", 'G');
	add_led(NULL, "cucumber:sync:clock-0");
}

static void __init gen_pickle_leds(void)
{
	static const char *const logo[] = { "red", "green", "blue" };
	static const char *const ring[] = { "green", "red", "blue" }; /* GRB in the DT */
	int c, i;

	for (c = 0; c < 3; c++)
		for (i = 0; i < 6; i++)
			add_led("ledfb-logo", "pickle:%s:logo%d", logo[c], i);
	add_led("ledfb-logo-sync", "pickle:logo:sync");
	for (i = 0; i < 16; i++)
		for (c = 0; c < 3; c++)
			add_led("ledfb-ring", "pickle:%s:ring%d", ring[c], i);
	add_led("ledfb-ring-sync", "pickle:ring:sync0");
	add_led("ledfb-ring-sync", "pickle:ring:sync1");
	gen_clock("pickle", 'H');
	add_led(NULL, "pickle:sync:clock");
}

static const struct emu_board boards[] = {
	{
		.name = "bergamot",
		.model = "Yandex Bergamot EVT1",
		.compatible = "yandex,bergamot-evt1\0yandex,bergamot\0amlogic, axg",
		.compatible_len = sizeof("yandex,bergamot-evt1\0yandex,bergamot\0amlogic, axg"),
		.triggers = bergamot_triggers,
		.gen_leds = gen_bergamot_leds,
		.inputs = bergamot_inputs,
		.fbs = { { "logo", 6 } },
	},
	{
		.name = "pickle",
		.model = "Yandex Pickle EVT",
		.compatible = "yandex,pickle-evt\0yandex,pickle\0amlogic, a4",
		.compatible_len = sizeof("yandex,pickle-evt\0yandex,pickle\0amlogic, a4"),
		.triggers = pickle_triggers,
		.gen_leds = gen_pickle_leds,
		.inputs = pickle_inputs,
		.fbs = { { "logo", 6 }, { "ring", 16 } },
	},
};

/* The vendor's ledfb triggers drive LEDs from a framebuffer; here they only have to exist. */
static void __init register_triggers(void)
{
	const char *const *t;

	for (t = board->triggers; *t; t++) {
		struct led_trigger *trig;

		led_trigger_register_simple(*t, &trig);
	}
}

/* S61leds_brightness writes a colour calibration here (on the real LED chip's device). */
static ssize_t brightness_scale_show(struct device *dev, struct device_attribute *attr,
				     char *buf)
{
	ssize_t n;

	mutex_lock(&emu_lock);
	n = scnprintf(buf, PAGE_SIZE, "%s", brightness_scale);
	mutex_unlock(&emu_lock);
	return n;
}

static ssize_t brightness_scale_store(struct device *dev, struct device_attribute *attr,
				      const char *buf, size_t count)
{
	mutex_lock(&emu_lock);
	strlcpy(brightness_scale, buf, min(count + 1, sizeof(brightness_scale)));
	mutex_unlock(&emu_lock);
	return count;
}
static DEVICE_ATTR_RW(brightness_scale);

/* ---------------------------------------------------------------------------------------------- */
/* Input                                                                                           */

static struct device *__init find_or_make_parent(const char *name)
{
	struct platform_device *pdev;
	struct device *dev;

	if (!name)
		return NULL;
	/* QEMU's virt DT has its own "gpio-keys" node: reuse that (unbound) device. */
	dev = bus_find_device_by_name(&platform_bus_type, NULL, name);
	if (dev)
		return dev;
	pdev = platform_device_register_simple(name, PLATFORM_DEVID_NONE, NULL, 0);
	return IS_ERR(pdev) ? NULL : &pdev->dev;
}

static void __init add_input(const struct emu_input_def *d)
{
	struct input_dev *in;
	int i, ret;

	if (emu_ninputs >= ARRAY_SIZE(emu_inputs))
		return;
	in = input_allocate_device();
	if (!in)
		return;
	in->name = d->name;
	in->phys = d->phys;
	in->id.bustype = d->bustype;
	in->id.vendor = d->vendor;
	in->id.product = d->product;
	in->id.version = d->version;
	in->dev.parent = find_or_make_parent(d->parent);
	for (i = 0; i < MAX_CODES && d->keys[i]; i++)
		input_set_capability(in, EV_KEY, d->keys[i]);
	for (i = 0; i < MAX_CODES && d->sws[i]; i++)
		input_set_capability(in, EV_SW, d->sws[i] - 1);
	if (d->msc) {
		input_set_capability(in, EV_MSC, MSC_SERIAL);
		input_set_capability(in, EV_MSC, MSC_SCAN);
	}
	ret = input_register_device(in);
	if (ret) {
		pr_err("input %s: %d\n", d->name, ret);
		input_free_device(in);
		return;
	}
	emu_inputs[emu_ninputs++] = in;
}

/* Report <type> <code> <value> on the first device that has the capability. */
static int inject(unsigned int type, unsigned int code, int value)
{
	int i;

	for (i = 0; i < emu_ninputs; i++) {
		struct input_dev *in = emu_inputs[i];
		unsigned long *bits = type == EV_KEY ? in->keybit : in->swbit;

		if (code > (type == EV_KEY ? KEY_MAX : SW_MAX) || !test_bit(code, bits))
			continue;
		if (type == EV_KEY && test_bit(EV_MSC, in->evbit))
			input_event(in, EV_MSC, MSC_SCAN, code);
		input_event(in, type, code, value);
		input_sync(in);
		return 0;
	}
	return -ENODEV;
}

/* ---------------------------------------------------------------------------------------------- */
/* aml_info                                                                                        */

static ssize_t reboot_reason_show(struct class *c, struct class_attribute *attr, char *buf)
{
	ssize_t n;

	mutex_lock(&emu_lock);
	n = scnprintf(buf, PAGE_SIZE, "%s\n", reboot_reason);
	mutex_unlock(&emu_lock);
	return n;
}

static ssize_t reboot_reason_store(struct class *c, struct class_attribute *attr,
				   const char *buf, size_t count)
{
	mutex_lock(&emu_lock);
	strlcpy(reboot_reason, buf, min(count + 1, sizeof(reboot_reason)));
	strim(reboot_reason);
	mutex_unlock(&emu_lock);
	return count;
}
static CLASS_ATTR_RW(reboot_reason);

static struct attribute *aml_info_attrs[] = {
	&class_attr_reboot_reason.attr,
	NULL,
};
ATTRIBUTE_GROUPS(aml_info);

static struct class aml_info_class = {
	.name = "aml_info",
	.owner = THIS_MODULE,
	.class_groups = aml_info_groups,
};

/* ---------------------------------------------------------------------------------------------- */
/* ya_pd power supply (the vendor driver reports mV / mA, not the standard uV / uA)               */

static enum power_supply_property pd_props[] = {
	POWER_SUPPLY_PROP_ONLINE,
	POWER_SUPPLY_PROP_HEALTH,
	POWER_SUPPLY_PROP_VOLTAGE_NOW,
	POWER_SUPPLY_PROP_CURRENT_NOW,
};

static int pd_get(struct power_supply *psy, enum power_supply_property p,
		  union power_supply_propval *val)
{
	switch (p) {
	case POWER_SUPPLY_PROP_ONLINE:
		val->intval = 1;
		break;
	case POWER_SUPPLY_PROP_HEALTH:
		val->intval = pd_mv ? POWER_SUPPLY_HEALTH_GOOD : POWER_SUPPLY_HEALTH_DEAD;
		break;
	case POWER_SUPPLY_PROP_VOLTAGE_NOW:
		val->intval = pd_mv;
		break;
	case POWER_SUPPLY_PROP_CURRENT_NOW:
		val->intval = pd_mv ? pd_ma : 0;
		break;
	default:
		return -EINVAL;
	}
	return 0;
}

/* S30pd_controller requests a PDO by writing voltage_now; accept it if a contract exists. */
static int pd_set(struct power_supply *psy, enum power_supply_property p,
		  const union power_supply_propval *val)
{
	if (p != POWER_SUPPLY_PROP_VOLTAGE_NOW)
		return -EINVAL;
	if (pd_mv)
		pd_mv = val->intval;
	return 0;
}

static int pd_writeable(struct power_supply *psy, enum power_supply_property p)
{
	return p == POWER_SUPPLY_PROP_VOLTAGE_NOW;
}

/* S30pd_controller only drives the PD sink if <ya_pd>/device/name says it is a HUSB238. */
static ssize_t name_show(struct device *dev, struct device_attribute *attr, char *buf)
{
	return sprintf(buf, "husb238_pd\n");
}
static DEVICE_ATTR_RO(name);

static const struct power_supply_desc pd_desc = {
	.name = "ya_pd",
	.type = POWER_SUPPLY_TYPE_USB_PD,
	.properties = pd_props,
	.num_properties = ARRAY_SIZE(pd_props),
	.get_property = pd_get,
	.set_property = pd_set,
	.property_is_writeable = pd_writeable,
};

/* ---------------------------------------------------------------------------------------------- */
/* ltr308als01 light sensor (in_intensity_raw / _input / _scale / _hardwaregain / _integration_time) */

static const struct iio_chan_spec lux_chan[] = {
	{
		.type = IIO_INTENSITY,
		.info_mask_separate = BIT(IIO_CHAN_INFO_RAW) | BIT(IIO_CHAN_INFO_PROCESSED) |
				      BIT(IIO_CHAN_INFO_SCALE) | BIT(IIO_CHAN_INFO_OFFSET) |
				      BIT(IIO_CHAN_INFO_HARDWAREGAIN) | BIT(IIO_CHAN_INFO_INT_TIME),
	},
};

static int lux_read(struct iio_dev *indio, struct iio_chan_spec const *chan, int *val,
		    int *val2, long mask)
{
	u64 p;

	switch (mask) {
	case IIO_CHAN_INFO_RAW:
		*val = lux_raw;
		return IIO_VAL_INT;
	case IIO_CHAN_INFO_PROCESSED:
		p = (u64)lux_raw * lux_scale_micro;
		*val = div_u64_rem(p, 1000000, (u32 *)val2);
		return IIO_VAL_INT_PLUS_MICRO;
	case IIO_CHAN_INFO_SCALE:
		*val = lux_scale_micro / 1000000;
		*val2 = lux_scale_micro % 1000000;
		return IIO_VAL_INT_PLUS_MICRO;
	case IIO_CHAN_INFO_OFFSET:
		*val = lux_offset;
		return IIO_VAL_INT;
	case IIO_CHAN_INFO_HARDWAREGAIN:
		*val = lux_gain;
		return IIO_VAL_INT;
	case IIO_CHAN_INFO_INT_TIME:
		*val = lux_itime_us / 1000000;
		*val2 = lux_itime_us % 1000000;
		return IIO_VAL_INT_PLUS_MICRO;
	}
	return -EINVAL;
}

static int lux_write(struct iio_dev *indio, struct iio_chan_spec const *chan, int val,
		     int val2, long mask)
{
	switch (mask) {
	case IIO_CHAN_INFO_HARDWAREGAIN:
		if (val < 0)
			return -EINVAL;
		lux_gain = val;
		return 0;
	case IIO_CHAN_INFO_INT_TIME:
		if (val < 0 || val2 < 0)
			return -EINVAL;
		lux_itime_us = val * 1000000 + val2;
		return 0;
	case IIO_CHAN_INFO_SCALE:
		if (val < 0 || val2 < 0)
			return -EINVAL;
		lux_scale_micro = val * 1000000 + val2;
		return 0;
	case IIO_CHAN_INFO_OFFSET:
		lux_offset = val;
		return 0;
	}
	return -EINVAL;
}

/* Writes parse as INT_PLUS_MICRO (the iio default), so "0.0" offsets from S80light-sensor work. */
static const struct iio_info lux_info = {
	.read_raw = lux_read,
	.write_raw = lux_write,
};

static void __init add_lux(void)
{
	struct iio_dev *indio;
	int ret;

	indio = iio_device_alloc(0);
	if (!indio)
		return;
	indio->name = "ltr308als01";
	indio->dev.parent = &emu_pdev->dev;
	indio->info = &lux_info;
	indio->channels = lux_chan;
	indio->num_channels = ARRAY_SIZE(lux_chan);
	indio->modes = INDIO_DIRECT_MODE;
	ret = iio_device_register(indio);
	if (ret) {
		pr_err("iio: %d\n", ret);
		iio_device_free(indio);
	}
}

/* ---------------------------------------------------------------------------------------------- */
/* ledfb framebuffers: <label>-framebuffer platform device -> fbN named <label>, plus a backlight */

static struct fb_ops ledfb_ops = {	/* not const: 5.4 fb_info.fbops is non-const */
	.owner = THIS_MODULE,
};

static int ledfb_bl_update(struct backlight_device *bl)
{
	return 0;
}

static const struct backlight_ops ledfb_bl_ops = {
	.update_status = ledfb_bl_update,
};

static void __init add_ledfb(const char *label, int leds)
{
	struct backlight_properties props = { .type = BACKLIGHT_RAW, .max_brightness = 255 };
	struct platform_device *pdev;
	struct fb_info *info;
	char name[32];
	void *mem;

	snprintf(name, sizeof(name), "%s-framebuffer", label);
	pdev = platform_device_register_simple(name, PLATFORM_DEVID_NONE, NULL, 0);
	if (IS_ERR(pdev))
		return;
	snprintf(name, sizeof(name), "%s-backlight", label);
	backlight_device_register(name, &pdev->dev, NULL, &ledfb_bl_ops, &props);

	info = framebuffer_alloc(0, &pdev->dev);
	mem = alloc_pages_exact(PAGE_SIZE, GFP_KERNEL | __GFP_ZERO);
	if (!info || !mem)
		return;
	strlcpy(info->fix.id, label, sizeof(info->fix.id));
	info->fix.type = FB_TYPE_PACKED_PIXELS;
	info->fix.visual = FB_VISUAL_TRUECOLOR;
	info->fix.line_length = leds * 3;
	info->fix.smem_start = virt_to_phys(mem);
	info->fix.smem_len = PAGE_SIZE;
	info->var.xres = info->var.xres_virtual = leds;
	info->var.yres = info->var.yres_virtual = 1;
	info->var.bits_per_pixel = 24;
	info->var.red = (struct fb_bitfield){ 0, 8, 0 };
	info->var.green = (struct fb_bitfield){ 8, 8, 0 };
	info->var.blue = (struct fb_bitfield){ 16, 8, 0 };
	info->screen_base = mem;
	info->screen_size = leds * 3;
	info->fbops = &ledfb_ops;
	info->flags = FBINFO_DEFAULT | FBINFO_VIRTFB;
	if (register_framebuffer(info))
		pr_err("framebuffer %s failed\n", label);
}

/* ---------------------------------------------------------------------------------------------- */
/* Device tree identity                                                                            */

static void __init set_root_prop(struct device_node *root, const char *name, const void *value,
				 int len)
{
	struct property *p;

	p = kzalloc(sizeof(*p), GFP_KERNEL);
	if (!p)
		return;
	p->name = kstrdup(name, GFP_KERNEL);
	p->value = kmemdup(value, len, GFP_KERNEL);
	p->length = len;
	if (!p->name || !p->value || of_update_property(root, p))
		pr_err("cannot set /%s\n", name);
}

static void __init set_identity(void)
{
	struct device_node *root = of_find_node_by_path("/");

	if (!root) {
		pr_warn("no device tree root; /proc/device-tree/compatible not set\n");
		return;
	}
	set_root_prop(root, "compatible", board->compatible, board->compatible_len);
	set_root_prop(root, "model", board->model, strlen(board->model) + 1);
	of_node_put(root);
}

/* ---------------------------------------------------------------------------------------------- */
/* debugfs                                                                                         */

/* "key": "<code> <value>" reports one event; "<code>" alone is a press + release. */
static ssize_t ev_write(struct file *f, const char __user *ubuf, size_t len, loff_t *ppos,
			unsigned int type)
{
	char buf[32];
	unsigned int code;
	int value, n, ret;

	if (len >= sizeof(buf))
		return -EINVAL;
	if (copy_from_user(buf, ubuf, len))
		return -EFAULT;
	buf[len] = 0;
	n = sscanf(buf, "%u %d", &code, &value);
	if (n < 1)
		return -EINVAL;
	if (n == 2) {
		ret = inject(type, code, value);
	} else if (type == EV_KEY) {
		ret = inject(type, code, 1);
		if (!ret)
			ret = inject(type, code, 0);
	} else {
		return -EINVAL;
	}
	return ret ? ret : len;
}

static ssize_t key_write(struct file *f, const char __user *b, size_t l, loff_t *p)
{
	return ev_write(f, b, l, p, EV_KEY);
}

static ssize_t sw_write(struct file *f, const char __user *b, size_t l, loff_t *p)
{
	return ev_write(f, b, l, p, EV_SW);
}

static const struct file_operations key_fops = {
	.owner = THIS_MODULE,
	.write = key_write,
};

static const struct file_operations sw_fops = {
	.owner = THIS_MODULE,
	.write = sw_write,
};

static int leds_show(struct seq_file *s, void *unused)
{
	struct emu_led *l;

	list_for_each_entry(l, &emu_leds, list)
		seq_printf(s, "%s %d\n", l->name, l->cdev.brightness);
	return 0;
}
DEFINE_SHOW_ATTRIBUTE(leds);

static int board_show(struct seq_file *s, void *unused)
{
	seq_printf(s, "%s\n", board->name);
	return 0;
}
DEFINE_SHOW_ATTRIBUTE(board);

static ssize_t rr_read(struct file *f, char __user *ubuf, size_t len, loff_t *ppos)
{
	char buf[sizeof(reboot_reason) + 1];
	int n;

	mutex_lock(&emu_lock);
	n = scnprintf(buf, sizeof(buf), "%s\n", reboot_reason);
	mutex_unlock(&emu_lock);
	return simple_read_from_buffer(ubuf, len, ppos, buf, n);
}

static ssize_t rr_write(struct file *f, const char __user *ubuf, size_t len, loff_t *ppos)
{
	char buf[sizeof(reboot_reason)];

	if (len >= sizeof(buf))
		return -EINVAL;
	if (copy_from_user(buf, ubuf, len))
		return -EFAULT;
	buf[len] = 0;
	mutex_lock(&emu_lock);
	strlcpy(reboot_reason, strim(buf), sizeof(reboot_reason));
	mutex_unlock(&emu_lock);
	return len;
}

static const struct file_operations rr_fops = {
	.owner = THIS_MODULE,
	.read = rr_read,
	.write = rr_write,
};

static void __init add_debugfs(void)
{
	struct dentry *d = debugfs_create_dir("emuboard", NULL);

	emu_debugfs = d;
	debugfs_create_file("key", 0200, d, NULL, &key_fops);
	debugfs_create_file("switch", 0200, d, NULL, &sw_fops);
	debugfs_create_file("leds", 0444, d, NULL, &leds_fops);
	debugfs_create_bool("led_log", 0644, d, &led_log);
	debugfs_create_file("reboot_reason", 0644, d, NULL, &rr_fops);
	debugfs_create_u32("pd_mv", 0644, d, &pd_mv);
	debugfs_create_u32("pd_ma", 0644, d, &pd_ma);
	debugfs_create_u32("lux_raw", 0644, d, &lux_raw);
	debugfs_create_file("board", 0444, d, NULL, &board_fops);
}

/* ---------------------------------------------------------------------------------------------- */

static int __init emuboard_init(void)
{
	const struct emu_input_def *d;
	struct power_supply_config cfg = { };
	struct platform_device *pd_pdev;
	struct power_supply *psy;
	int i, ret;

	if (!emu_board_name[0])
		return 0;
	for (i = 0; i < ARRAY_SIZE(boards); i++)
		if (!strcmp(boards[i].name, emu_board_name))
			board = &boards[i];
	if (!board) {
		pr_err("unknown board \"%s\"\n", emu_board_name);
		return -EINVAL;
	}

	emu_pdev = platform_device_register_simple("emuboard", PLATFORM_DEVID_NONE, NULL, 0);
	if (IS_ERR(emu_pdev))
		return PTR_ERR(emu_pdev);
	ret = device_create_file(&emu_pdev->dev, &dev_attr_brightness_scale);
	if (ret)
		pr_warn("brightness_scale: %d\n", ret);

	set_identity();

	/* Inputs first, in the real order: nothing else in this kernel creates input devices. */
	for (d = board->inputs; d->name; d++)
		add_input(d);

	register_triggers();
	board->gen_leds();

	ret = class_register(&aml_info_class);
	if (ret)
		pr_warn("aml_info: %d\n", ret);

	pd_pdev = platform_device_register_simple("husb238", PLATFORM_DEVID_NONE, NULL, 0);
	if (!IS_ERR(pd_pdev)) {
		if (device_create_file(&pd_pdev->dev, &dev_attr_name))
			pr_warn("husb238 name attribute failed\n");
		psy = power_supply_register(&pd_pdev->dev, &pd_desc, &cfg);
		if (IS_ERR(psy))
			pr_warn("ya_pd: %ld\n", PTR_ERR(psy));
	}

	add_lux();
	for (i = 0; i < ARRAY_SIZE(board->fbs) && board->fbs[i].label; i++)
		add_ledfb(board->fbs[i].label, board->fbs[i].leds);
	add_debugfs();

	pr_info("board %s: %d LEDs, %d input devices, reboot_reason %s\n",
		board->name, emu_nleds, emu_ninputs, reboot_reason);
	return 0;
}
device_initcall(emuboard_init);
