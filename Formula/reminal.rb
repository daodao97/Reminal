class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.3"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.3/reminal_2.1.3_darwin_arm64.tar.gz"
      sha256 "3126bd90164fa32e4638e89ee70f646bd60c5ae50adf4bb2edde672e74ec0d1a"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.3/reminal_2.1.3_darwin_amd64.tar.gz"
      sha256 "1501bdc66023e1d24f03bdfcb698213098aa9f5f66a98ecf174851a6663cc0d5"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.3/reminal_2.1.3_linux_arm64.tar.gz"
      sha256 "3dfe4316364068b2ef162d0bc7c5d64b7aa3da1c4f3a8fea4d427ea82c846041"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.3/reminal_2.1.3_linux_amd64.tar.gz"
      sha256 "823a640e4e26cbc26d3c9515c1c0479b85f45294d929b5604940c0109e7dd927"
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
