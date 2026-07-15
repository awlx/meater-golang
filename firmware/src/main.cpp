// MEATER BLE -> Ethernet bridge for the Olimex ESP32-POE-ISO.
//
// Why this exists: meater-golang is a normal Go program (SQLite, net/http,
// BlueZ) and cannot run on an ESP32 -- there is no OS to run it on. But the
// ESP32-POE-ISO is an ideal *radio*: PoE means one cable to a spot in BLE range
// of the grill. So the board does exactly one job -- hold the GATT link to the
// probe and forward its notifications over TCP -- while the Go program does the
// decoding, history, ETA and dashboard on a real host.
//
// Deliberately NOT decoded here. The probe's payload format (the /32 scale, the
// ambient sensor at byte offset 10) is calibrated in internal/meater/meater.go
// and validated against the official app. Re-implementing that in C++ would
// fork the most fragile logic in the project across two languages and two
// release cycles. We ship raw bytes; Go stays the single source of truth.
//
// Protocol (ASCII, one \n-terminated line per message, Go is the TCP client):
//
//   T <hex>          raw temperature characteristic payload, hex encoded
//   S connected      GATT link to the probe is live
//   S disconnected   probe lost; we keep rescanning
//   # <text>         banner/log, ignored by the client
//
// The Go side dials us on Start and hangs up on Stop, so we scan for the probe
// only while a client is attached: no client, no radio traffic.

#include <Arduino.h>
#include <ETH.h>
#include <WiFi.h>  // WiFiServer/WiFiClient + the shared event loop ETH rides on
#include <NimBLEDevice.h>

// Must match internal/meater/meater.go.
static constexpr char kProbeNamePrefix[] = "MEATER";
static constexpr char kServiceUUID[] = "c9e2746c-59f1-4e54-a0dd-e1e54555cf8b";
static constexpr char kTemperatureCharUUID[] = "7edda774-045e-4bbf-909b-45d1991a2876";

static constexpr uint16_t kListenPort = 9000;
static constexpr uint32_t kScanDurationMs = 10000;

// The probe's payload is 12 bytes today; allow headroom for future firmware
// without risking a stack blowout in the notify callback.
static constexpr size_t kMaxPayload = 32;

struct Payload {
    uint8_t data[kMaxPayload];
    uint8_t len;
};

// Notifications arrive on the NimBLE host task, but the socket is owned by
// loop(). Hand payloads across on a queue rather than touching the client from
// two tasks -- lwIP sockets are not reentrant.
static QueueHandle_t payloadQueue = nullptr;

static WiFiServer server(kListenPort);
static NimBLEClient *probeClient = nullptr;
static volatile bool probeConnected = false;
static bool ethConnected = false;

// onNotify runs on the NimBLE task: copy and post, never block or print.
static void onNotify(NimBLERemoteCharacteristic *chr, uint8_t *data, size_t len, bool isNotify) {
    if (len == 0 || len > kMaxPayload) {
        return;
    }
    Payload p;
    memcpy(p.data, data, len);
    p.len = static_cast<uint8_t>(len);
    // Drop rather than block: a stalled socket must not wedge the BLE stack.
    xQueueSend(payloadQueue, &p, 0);
}

class ProbeCallbacks : public NimBLEClientCallbacks {
    void onConnect(NimBLEClient *c) override { probeConnected = true; }
    void onDisconnect(NimBLEClient *c, int reason) override {
        probeConnected = false;
        Serial.printf("probe disconnected (reason %d)\n", reason);
    }
};
static ProbeCallbacks probeCallbacks;

// Ethernet is the whole point of this board, so narrate every stage of bringing
// it up. Without this, "no cable", "PHY never initialised" and "DHCP timed out"
// all look identical from the serial log: silence.
static void onEthEvent(WiFiEvent_t event) {
    switch (event) {
    case ARDUINO_EVENT_ETH_START:
        Serial.println("ethernet: PHY started (LAN8720 alive on GPIO12 power)");
        ETH.setHostname("meater-bridge");
        break;
    case ARDUINO_EVENT_ETH_CONNECTED:
        Serial.println("ethernet: link up, requesting DHCP lease...");
        break;
    case ARDUINO_EVENT_ETH_GOT_IP:
        Serial.printf("ethernet up: %s (%uMbps, %s)\n", ETH.localIP().toString().c_str(),
                      ETH.linkSpeed(), ETH.fullDuplex() ? "full duplex" : "half duplex");
        ethConnected = true;
        break;
    case ARDUINO_EVENT_ETH_DISCONNECTED:
        Serial.println("ethernet: link down (cable unplugged?)");
        ethConnected = false;
        break;
    case ARDUINO_EVENT_ETH_STOP:
        Serial.println("ethernet: stopped");
        ethConnected = false;
        break;
    default:
        break;
    }
}

// findProbe scans for a MEATER* advertisement and returns it, or nullptr.
static const NimBLEAdvertisedDevice *findProbe() {
    NimBLEScan *scan = NimBLEDevice::getScan();
    scan->setActiveScan(true);  // we match on the name, which needs a scan response
    NimBLEScanResults results = scan->getResults(kScanDurationMs, false);

    for (int i = 0; i < results.getCount(); i++) {
        const NimBLEAdvertisedDevice *dev = results.getDevice(i);
        if (dev->haveName() && dev->getName().rfind(kProbeNamePrefix, 0) == 0) {
            Serial.printf("found %s (%s)\n", dev->getName().c_str(), dev->getAddress().toString().c_str());
            return dev;
        }
    }
    return nullptr;
}

// connectProbe attaches to the probe and subscribes to its temperature
// characteristic. Returns false on any failure; the caller simply retries.
static bool connectProbe(const NimBLEAdvertisedDevice *dev) {
    if (probeClient == nullptr) {
        probeClient = NimBLEDevice::createClient();
        probeClient->setClientCallbacks(&probeCallbacks, false);
    }

    if (!probeClient->connect(dev)) {
        Serial.println("connect failed");
        return false;
    }

    // Locate the characteristic by UUID wherever it lives: like the Go BlueZ
    // path, we don't trust the probe to advertise the service we expect.
    NimBLERemoteCharacteristic *tempChar = nullptr;
    NimBLERemoteService *svc = probeClient->getService(kServiceUUID);
    if (svc != nullptr) {
        tempChar = svc->getCharacteristic(kTemperatureCharUUID);
    }
    if (tempChar == nullptr) {
        for (auto &s : probeClient->getServices(true)) {
            if (auto *c = s->getCharacteristic(kTemperatureCharUUID)) {
                tempChar = c;
                break;
            }
        }
    }

    if (tempChar == nullptr || !tempChar->canNotify()) {
        Serial.println("temperature characteristic not found/!notify");
        probeClient->disconnect();
        return false;
    }

    if (!tempChar->subscribe(true, onNotify)) {
        Serial.println("subscribe failed");
        probeClient->disconnect();
        return false;
    }

    Serial.println("subscribed, streaming");
    return true;
}

void setup() {
    Serial.begin(115200);
    Serial.println("\nmeater-bridge starting");

    payloadQueue = xQueueCreate(16, sizeof(Payload));

    WiFi.onEvent(onEthEvent);
    ETH.begin();  // pin map comes from the esp32-poe-iso variant header

    NimBLEDevice::init("meater-bridge");
    NimBLEDevice::setPower(ESP_PWR_LVL_P9);  // grill may be a room away

    server.begin();
    server.setNoDelay(true);
    Serial.printf("TCP server ready on :%u (reachable once ethernet has an IP)\n", kListenPort);
}

void loop() {
    if (!ethConnected) {
        // Say so periodically rather than idling silently: with USB power but no
        // cable the board looks identical to a working one, and "listening on
        // :9000" above is a lie until we actually have an address.
        static uint32_t lastNag = 0;
        if (millis() - lastNag > 5000) {
            lastNag = millis();
            Serial.println("waiting for ethernet (no IP yet; USB gives power+serial only)");
        }
        delay(200);
        return;
    }

    WiFiClient client = server.available();
    if (!client) {
        delay(50);
        return;
    }

    Serial.printf("client %s attached\n", client.remoteIP().toString().c_str());
    client.printf("# meater-bridge on %s\n", ETH.localIP().toString().c_str());

    xQueueReset(payloadQueue);
    // -1 means "nothing reported yet", so the first pass always states the
    // probe's status explicitly. The client must never have to infer it.
    int reported = -1;

    // Serve exactly one client: the Go side is a single consumer, and one GATT
    // link to the probe is all the hardware allows anyway.
    while (client.connected()) {
        if (!probeConnected) {
            if (reported != 0) {
                client.print("S disconnected\n");
                reported = 0;
            }
            // Keepalive before every scan window. Without it the socket goes
            // silent for the whole scan, which the client cannot tell apart
            // from a wedged board -- it would hang up and redial, aborting this
            // very scan, and loop forever whenever the probe is absent. A probe
            // that is simply not here yet is normal, not an error.
            client.print("# scanning for a MEATER probe\n");

            // Scanning blocks for kScanDurationMs; check the client is still
            // there afterwards before spending another window on it.
            const NimBLEAdvertisedDevice *dev = findProbe();
            if (!client.connected()) {
                break;
            }
            if (dev == nullptr || !connectProbe(dev)) {
                continue;
            }
        }

        if (probeConnected && reported != 1) {
            client.print("S connected\n");
            reported = 1;
        }

        Payload p;
        while (xQueueReceive(payloadQueue, &p, pdMS_TO_TICKS(500)) == pdTRUE) {
            // "T " + hex + "\n", built in one buffer so it lands in one write.
            char line[4 + kMaxPayload * 2];
            size_t n = 0;
            line[n++] = 'T';
            line[n++] = ' ';
            for (uint8_t i = 0; i < p.len; i++) {
                static const char kHex[] = "0123456789abcdef";
                line[n++] = kHex[p.data[i] >> 4];
                line[n++] = kHex[p.data[i] & 0x0f];
            }
            line[n++] = '\n';
            client.write(reinterpret_cast<const uint8_t *>(line), n);
            if (!client.connected()) {
                break;
            }
        }
    }

    Serial.println("client gone, releasing probe");
    client.stop();
    if (probeClient != nullptr && probeClient->isConnected()) {
        probeClient->disconnect();
    }
    // Let the disconnect settle before we accept again.
    delay(200);
}
