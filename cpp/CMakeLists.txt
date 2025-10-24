cmake_minimum_required(VERSION 3.10)
project(hookbridge)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(hookbridge SHARED
    hook_bridge.cpp
    art_hooks.cpp
    niantic_hooks.cpp
)

target_include_directories(hookbridge PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_link_libraries(hookbridge dl)
