#include "hook_bridge.h"
#include "art_hooks.h"
#include "niantic_hooks.h"
#include <iostream>

void InitHooks() {
    std::cout << "[RotomWorker] Inicializando hooks..." << std::endl;

    InitArtHooks();
    InitNianticHooks();

    std::cout << "[RotomWorker] Hooks aplicados com sucesso!" << std::endl;
}
