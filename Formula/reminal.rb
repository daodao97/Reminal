class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.10.3"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.3/reminal_1.10.3_darwin_arm64.tar.gz"
      sha256 "b48c54f0729fcd4669530086e7b6f34e03d64235b6be4008671490219aa3c9b3"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.3/reminal_1.10.3_darwin_amd64.tar.gz"
      sha256 "99962328dd4c06e398f7888f7d20258fea832efd130a93a500f871c0aeb7b22b"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.3/reminal_1.10.3_linux_arm64.tar.gz"
      sha256 "3398db528fd05c71e42b67d3db3e251619861e7aeea32c82edb6a1a289ef51a1"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.3/reminal_1.10.3_linux_amd64.tar.gz"
      sha256 "788fc16f70505993dbcb4c26dc0c67ad7c5efbf68c0e9a4df7725af3b002d9a0"
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
