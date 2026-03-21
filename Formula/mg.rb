# Homebrew formula for macguffin (mg)
# Install: brew install drellem2/macguffin/mg
# Or add the tap first: brew tap drellem2/macguffin && brew install mg
class Mg < Formula
  desc "macguffin work-item tracker"
  homepage "https://github.com/drellem2/macguffin"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/drellem2/macguffin/releases/download/v#{version}/mg_darwin_arm64"
      sha256 "9f4d86f52063aa3aab714ce51368bd956b5d0daf37b0aea7bf7ca133d6d66cf2"
    end
    on_intel do
      url "https://github.com/drellem2/macguffin/releases/download/v#{version}/mg_darwin_amd64"
      sha256 "3497876803a19c2b4e3fcf8bc1169ed7c79db3e15d4360b63375b849713e8e62"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/drellem2/macguffin/releases/download/v#{version}/mg_linux_arm64"
      sha256 "c4b5a57d8e06ca0519d728c50595f2196e93d4a37f4ead325b899a56451dcf76"
    end
    on_intel do
      url "https://github.com/drellem2/macguffin/releases/download/v#{version}/mg_linux_amd64"
      sha256 "dd1fc3d185c2810ceea4f3b9e3799d3f79d87d3dd88490570ae9d8d5dea8ee7d"
    end
  end

  def install
    binary = Dir["mg_*"].first || "mg"
    bin.install binary => "mg"
  end

  test do
    assert_match "mg v#{version}", shell_output("#{bin}/mg version")
  end
end
