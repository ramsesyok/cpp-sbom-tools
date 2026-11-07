# paho.mqtt.c を静的リンクする版（Make）
CXX ?= g++
CXXFLAGS = -O2 -std=c++17 -Ithird_party -Isrc
build/mqtt_sample_c: src/main_c.cpp
	mkdir -p build
	$(CXX) $(CXXFLAGS) -c src/main_c.cpp -o build/main_c.o
	$(CXX) build/main_c.o -Wl,-Bstatic -lpaho-mqtt3c -Wl,-Bdynamic -lpthread -o build/mqtt_sample_c
clean:
	rm -rf build
