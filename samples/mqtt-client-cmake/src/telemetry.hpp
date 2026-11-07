// 自社コード（サンプル）: センサー値をJSONにする
#pragma once
#include <string>
#include "nlohmann/json.hpp"

namespace sample {
inline std::string make_payload(const std::string& device, double temperature) {
    nlohmann::json j;
    j["device"] = device;
    j["temperature"] = temperature;
    j["unit"] = "C";
    return j.dump();
}
}  // namespace sample
