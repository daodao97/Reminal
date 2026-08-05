class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.12.5"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.5/reminal_1.12.5_darwin_arm64.tar.gz"
      sha256 "3bbfb8ae3d53402b53e191a1a3d8f50d7843c47d58de07b9542b938a4182fef5"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.5/reminal_1.12.5_darwin_amd64.tar.gz"
      sha256 "98119a3f10ae3359af92df0a7e65a30fd60876fccd3a869ea3967cb7eb536a73"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.5/reminal_1.12.5_linux_arm64.tar.gz"
      sha256 "04021182fb3a1475ec9489347ae46965c4e13cec4748f63f4616965b59efb720"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.5/reminal_1.12.5_linux_amd64.tar.gz"
      sha256 "b270097fc17e86c9753ebf8d3f70046086e0547737ad311b8558a2ca82ed2344"
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
