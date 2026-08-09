class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.4"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.4/reminal_2.1.4_darwin_arm64.tar.gz"
      sha256 "fefe48e3dedc02d687d5c63ac35dd9d700057111014faf424da675dcd09a7e97"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.4/reminal_2.1.4_darwin_amd64.tar.gz"
      sha256 "365f543fcc1b71f568c646876ba1182e1783771a03b4a19378dfd29aadb0deea"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.4/reminal_2.1.4_linux_arm64.tar.gz"
      sha256 "cb3e9a60204d64fcff571cef4455bd0cf265e4b1373c67840ffd151fa4cc5836"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.4/reminal_2.1.4_linux_amd64.tar.gz"
      sha256 "24fd65dcd122e5c0d4fec5d5232b553c0230ad1b065fb82ee7b3e491ddf7ac99"
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
