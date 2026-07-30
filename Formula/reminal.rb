class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.11.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.11.0/reminal_1.11.0_darwin_arm64.tar.gz"
      sha256 "26c1841f992ebb1cccc171fe841e653044d7f46fe120ba3712610920960e0e02"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.11.0/reminal_1.11.0_darwin_amd64.tar.gz"
      sha256 "d92efe9a7966a9f1b7c05231f73bd5ea7c0a1caf6e72df80c8522314cb2b1d5a"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.11.0/reminal_1.11.0_linux_arm64.tar.gz"
      sha256 "52dbf280248917f9a32fb955cde833efaad4012e56c49c3ef8b8febf8b580bf3"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.11.0/reminal_1.11.0_linux_amd64.tar.gz"
      sha256 "8dfe728673e71ea41c348271561895f49a57b361b809bd55a26e7ea05036cf5b"
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
