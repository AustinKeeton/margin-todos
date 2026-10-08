// qrcdump: LD_PRELOAD into a Qt app to save every compiled-in resource as it registers.
// Writes /tmp/qrc-dump/<path>[.zlib|.zstd] (compressed entries are saved raw; decompress offline).
// For local reference only: the dumped files belong to the app's vendor.
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <dlfcn.h>
#include <string>
#include <sys/stat.h>

namespace {
uint32_t be32(const unsigned char* p) { return (p[0] << 24) | (p[1] << 16) | (p[2] << 8) | p[3]; }
uint16_t be16(const unsigned char* p) { return (p[0] << 8) | p[1]; }

void mkdirs(const std::string& path) {
  for (size_t i = 1; i < path.size(); i++) {
    if (path[i] == '/') { mkdir(path.substr(0, i).c_str(), 0755); }
  }
}

std::string nameAt(const unsigned char* names, uint32_t off) {
  const unsigned char* p = names + off;
  const uint16_t len = be16(p);
  std::string s;
  for (uint16_t i = 0; i < len; i++) {
    const uint16_t c = be16(p + 6 + 2 * i);
    s += c < 0x80 ? char(c) : '_';
  }
  return s;
}

void walk(int version, const unsigned char* tree, const unsigned char* names,
          const unsigned char* data, uint32_t node, const std::string& path, int depth) {
  if (depth > 64) return;
  const size_t nodeSize = version >= 2 ? 22 : 14;
  const unsigned char* n = tree + node * nodeSize;
  const uint16_t flags = be16(n + 4);
  const std::string name = node == 0 ? "" : nameAt(names, be32(n));
  const std::string here = path + (node == 0 ? "" : "/" + name);
  if (flags & 0x02) { // directory
    const uint32_t count = be32(n + 6), first = be32(n + 10);
    for (uint32_t i = 0; i < count; i++) walk(version, tree, names, data, first + i, here, depth + 1);
    return;
  }
  const unsigned char* d = data + be32(n + 10);
  const uint32_t size = be32(d);
  const char* ext = (flags & 0x01) ? ".zlib" : (flags & 0x04) ? ".zstd" : "";
  const std::string out = "/tmp/qrc-dump" + here + ext;
  mkdirs(out);
  if (FILE* f = std::fopen(out.c_str(), "wb")) { std::fwrite(d + 4, 1, size, f); std::fclose(f); }
}
} // namespace

// bool qRegisterResourceData(int, const unsigned char*, const unsigned char*, const unsigned char*)
extern "C" bool _Z21qRegisterResourceDataiPKhS0_S0_(int version, const unsigned char* tree,
                                                    const unsigned char* names,
                                                    const unsigned char* data) {
  using Fn = bool (*)(int, const unsigned char*, const unsigned char*, const unsigned char*);
  static Fn real = reinterpret_cast<Fn>(dlsym(RTLD_NEXT, "_Z21qRegisterResourceDataiPKhS0_S0_"));
  walk(version, tree, names, data, 0, "", 0);
  return real(version, tree, names, data);
}
