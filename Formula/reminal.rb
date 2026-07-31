class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.12.1"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.1/reminal_1.12.1_darwin_arm64.tar.gz"
      sha256 "72157a3e268fba2fbc42f5bc49df4ed3e8626a72ec6828f9708828fd5235e3b2"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.1/reminal_1.12.1_darwin_amd64.tar.gz"
      sha256 "9a62b0c53bdb3f08405c67b1d4109be517c85d53b08f9a99878b9e2402830884"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.1/reminal_1.12.1_linux_arm64.tar.gz"
      sha256 "95fb3e5620f04f787bffe4397e1ed9bf0d8fda84ea620e81e4c9548278c66ffb"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.1/reminal_1.12.1_linux_amd64.tar.gz"
      sha256 "1f538a7680a9e089788c67cb69a209000cc2a6e94c5c76163818d9c4e4c15a6a"
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
