// 自社コード（サンプル）: paho.mqtt.cpp でJSONを送信するMQTTクライアント
#include <iostream>
#include <mqtt/async_client.h>
#include "telemetry.hpp"

int main(int argc, char* argv[]) {
    const std::string server = argc > 1 ? argv[1] : "tcp://localhost:1883";
    mqtt::async_client client(server, "sample-client");
    auto opts = mqtt::connect_options_builder().clean_session().finalize();
    try {
        client.connect(opts)->wait();
        auto payload = sample::make_payload("sensor-01", 23.5);
        client.publish("sample/telemetry", payload, 1, false)->wait();
        std::cout << "published: " << payload << std::endl;
        client.disconnect()->wait();
    } catch (const mqtt::exception& e) {
        std::cerr << "MQTT error: " << e.what() << std::endl;
        return 1;
    }
    return 0;
}
