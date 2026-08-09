class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.8"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.8/reminal_2.1.8_darwin_arm64.tar.gz"
      sha256 "29ee1d333596b2d2b43c8d28389aec2210487cf51752dd7f3e48a21c2fb594f4"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.8/reminal_2.1.8_darwin_amd64.tar.gz"
      sha256 "c88cbbd307bdbc5921a57fba4e08a7966c9d0069dab4df514403b5fa259d7842"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.8/reminal_2.1.8_linux_arm64.tar.gz"
      sha256 "53cc7e21ec21fee01bb9787dade33ff321efa8b49a6f8cc3a5b66e699edb860f"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.8/reminal_2.1.8_linux_amd64.tar.gz"
      sha256 "646628bc910821065698d8a588754103c2bf6c42091114b05d439f416bfec117"
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
