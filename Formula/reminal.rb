class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.6"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.6/reminal_2.1.6_darwin_arm64.tar.gz"
      sha256 "1f6486c3d9c1c6e8ec0d047c1d8be89489f4f6a4390eb737ab5a8c3ede5c67b0"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.6/reminal_2.1.6_darwin_amd64.tar.gz"
      sha256 "95c9f24948f2de7b633f1321140e3421bf595821ccaaf2669c1cdb254de0d9c1"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.6/reminal_2.1.6_linux_arm64.tar.gz"
      sha256 "48f9cbf80c8881f47885e15731d4e56f2dc4e94fe202a204e51f3997f8ef4869"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.6/reminal_2.1.6_linux_amd64.tar.gz"
      sha256 "5b4a0621d0454f368987848a2d835d8792857f5cb641bee550c13bb7d0f71f13"
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
