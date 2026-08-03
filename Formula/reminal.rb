class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.12.3"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.3/reminal_1.12.3_darwin_arm64.tar.gz"
      sha256 "2099227df1ae5016ba42d37f04534556e14b7474afda79512fb52aede541de2f"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.3/reminal_1.12.3_darwin_amd64.tar.gz"
      sha256 "e2af9788a7479decf527187a0934277feac99260188591285a4f74db83bf6645"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.3/reminal_1.12.3_linux_arm64.tar.gz"
      sha256 "fbd2de64573f82f6cc7c977ab9ee2d34390f822c45847b60ce02e0a817959f15"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.3/reminal_1.12.3_linux_amd64.tar.gz"
      sha256 "b014b2d3777557cc0e1c13d11e1d281cd4b728aa8e6b3e9766dd808fc4f1534a"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
