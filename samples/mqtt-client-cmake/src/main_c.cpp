// 自社コード（サンプル）: paho.mqtt.c（同期API）でJSONを送信。静的リンク版
#include <cstdio>
#include <cstring>
#include <MQTTClient.h>
#include "telemetry.hpp"

int main(int argc, char* argv[]) {
    const char* server = argc > 1 ? argv[1] : "tcp://localhost:1883";
    MQTTClient client;
    MQTTClient_create(&client, server, "sample-client-c", MQTTCLIENT_PERSISTENCE_NONE, nullptr);
    MQTTClient_connectOptions opts = MQTTClient_connectOptions_initializer;
    opts.cleansession = 1;
    if (MQTTClient_connect(client, &opts) != MQTTCLIENT_SUCCESS) {
        std::fprintf(stderr, "connect failed\n");
        MQTTClient_destroy(&client);
        return 1;
    }
    std::string payload = sample::make_payload("sensor-02", 24.0);
    MQTTClient_message msg = MQTTClient_message_initializer;
    msg.payload = (void*)payload.c_str();
    msg.payloadlen = (int)payload.size();
    msg.qos = 1;
    MQTTClient_deliveryToken token;
    MQTTClient_publishMessage(client, "sample/telemetry", &msg, &token);
    MQTTClient_waitForCompletion(client, token, 5000);
    std::printf("published: %s\n", payload.c_str());
    MQTTClient_disconnect(client, 1000);
    MQTTClient_destroy(&client);
    return 0;
}
