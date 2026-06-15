// storage_flush.h — SD card flush task (plan.md §4.8.3).
#pragma once

#include <cstdint>
#include <string>

#include "psram_ring.h"
#include "sd_card.h"

namespace tether::m5 {

class StorageFlush {
public:
  StorageFlush(PsramRing &ring, SdCard &card);

  // Initialize. Returns true on success.
  bool Init();

  // Pump one cycle: drain a chunk from the ring into a file on SD.
  // Returns the number of bytes written.
  size_t RunOnce();

  // Force the writer to roll over the current output file.
  void RotateFileForTest();

  // Total bytes written to SD.
  uint64_t TotalBytesWritten() const { return total_bytes_; }
  // Total chunks written.
  uint64_t ChunksWritten() const { return chunks_written_; }
  // Test seam: return the most recent file path used.
  const std::string &LastFile() const { return last_file_; }

private:
  PsramRing &ring_;
  SdCard &card_;
  std::string last_file_;
  uint64_t total_bytes_ = 0;
  uint64_t chunks_written_ = 0;
};

} // namespace tether::m5
