class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.12.4"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.4/reminal_1.12.4_darwin_arm64.tar.gz"
      sha256 "b54dee2cea26c823b4e21e4704cd8ed7b43c894e05dfc6007abc39e4e794a4cc"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.4/reminal_1.12.4_darwin_amd64.tar.gz"
      sha256 "bce9a583afed6c7bacea688436ff9ae7409472c9aa1a20f8879f5fab5546e43b"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.4/reminal_1.12.4_linux_arm64.tar.gz"
      sha256 "03b19eec0fdffd491dbaa10dcc734127c4a8eefaefd04561fcccf7ba8e9b065e"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.4/reminal_1.12.4_linux_amd64.tar.gz"
      sha256 "d544b5245bb99b5debaa089ff6bbd42d752ea0101868230a71d7b3b644d4e616"
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
