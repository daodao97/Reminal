class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.1"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.1/reminal_2.0.1_darwin_arm64.tar.gz"
      sha256 "a4ef401683b2b611b598e2c09da7cad2f3d901b9c36f7945a22bf368c9adfea9"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.1/reminal_2.0.1_darwin_amd64.tar.gz"
      sha256 "fc3cb2ed5ee705974f2c9ca56b45c856448ad82ee53d193aedfda50925148844"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.1/reminal_2.0.1_linux_arm64.tar.gz"
      sha256 "c895e8c41b0050b9a3d9aa75425458e8c2d356a8bac43f7a05504c479fc058f5"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.1/reminal_2.0.1_linux_amd64.tar.gz"
      sha256 "2fce73758c4fe414c2dad121a0d531db1334993bf9306af5574e65face28a0e9"
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
